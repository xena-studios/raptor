// Package metrics keeps each server's resource history on the node
// (docs/WINGS.md#local-metrics): CPU, memory, network, disk, and players,
// sampled every 10 seconds, one row per minute for a day, then one per 15
// minutes for a week.
package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Timings and retention.
const (
	SampleEvery  = 10 * time.Second
	queryEvery   = 30 * time.Second
	Minute       = 60  // resolution, seconds
	QuarterHour  = 900 // resolution, seconds
	keepMinutes  = 24 * time.Hour
	keepQuarters = 7 * 24 * time.Hour
	rollupEvery  = time.Hour
)

// Servers is what the collector needs from the server manager.
type Servers interface {
	List() map[string]server.State
	Sample(ctx context.Context, id string) (server.Sample, error)
}

// Point is a server's resources over one bucket (or, for Latest, right now).
type Point struct {
	At         time.Time
	Resolution int // seconds; 0 for a live sample
	Samples    int // samples taken while the server ran
	CPUAvg     float64
	CPUMax     float64
	MemoryAvg  int64
	MemoryMax  int64
	RxBytes    int64 // traffic in the bucket; for a live sample, per second
	TxBytes    int64
	DiskBytes  int64
	PlayersAvg *float64
	PlayersMax *int
}

// Collector samples every server and writes their history.
type Collector struct {
	Servers Servers
	DB      *store.DB
	Log     *slog.Logger
	Now     func() time.Time
	// Query asks a game for its players (QueryPlayers by default).
	Query func(ctx context.Context, protocol, addr string) (Players, error)

	mu    sync.Mutex
	state map[string]*serverState
}

type serverState struct {
	last      server.Sample // the previous running sample, for rates
	bucket    bucket
	latest    Point
	players   *Players
	queriedAt time.Time
}

type bucket struct {
	at                  time.Time
	samples             int
	cpuSum, cpuMax      float64
	memSum, memMax      int64
	rx, tx              int64
	disk                int64
	playersSum          float64
	playersN, playersMx int
}

func (c *Collector) init() {
	if c.state == nil {
		c.state = map[string]*serverState{}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	if c.Query == nil {
		c.Query = QueryPlayers
	}
}

// Run samples until ctx ends, then writes the buckets in progress.
func (c *Collector) Run(ctx context.Context) {
	c.mu.Lock()
	c.init()
	c.mu.Unlock()
	t := time.NewTicker(SampleEvery)
	defer t.Stop()
	lastRollup := time.Time{}
	for {
		c.Tick(ctx)
		if c.Now().Sub(lastRollup) >= rollupEvery {
			if err := c.Rollup(ctx); err != nil && ctx.Err() == nil {
				c.Log.Error("rolling up metrics failed", "err", err)
			}
			lastRollup = c.Now()
		}
		select {
		case <-ctx.Done():
			c.Flush(context.WithoutCancel(ctx))
			return
		case <-t.C:
		}
	}
}

// Tick samples every server once.
func (c *Collector) Tick(ctx context.Context) {
	c.mu.Lock()
	c.init()
	c.mu.Unlock()
	ids := c.Servers.List()
	var wg sync.WaitGroup
	for id := range ids {
		wg.Go(func() { c.sample(ctx, id) })
	}
	wg.Wait()
	// Forget servers that are gone (their rows go with them).
	c.mu.Lock()
	for id := range c.state {
		if _, ok := ids[id]; !ok {
			delete(c.state, id)
		}
	}
	c.mu.Unlock()
}

func (c *Collector) sample(ctx context.Context, id string) {
	s, err := c.Servers.Sample(ctx, id)
	if err != nil {
		return
	}
	now := c.Now()
	c.mu.Lock()
	st := c.state[id]
	if st == nil {
		st = &serverState{}
		c.state[id] = st
	}
	query := s.Running && s.Query != "" && now.Sub(st.queriedAt) >= queryEvery
	if query {
		st.queriedAt = now
	}
	c.mu.Unlock()
	if query {
		p, err := c.Query(ctx, s.Query, s.QueryAddr)
		c.mu.Lock()
		if err == nil {
			st.players = &p
		} else {
			st.players = nil // starting, or not answering: unknown
		}
		c.mu.Unlock()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	start := now.Truncate(time.Minute)
	if !st.bucket.at.IsZero() && !st.bucket.at.Equal(start) {
		c.write(ctx, id, Minute, st.bucket)
		st.bucket = bucket{}
	}
	b := &st.bucket
	b.at, b.disk = start, s.DiskBytes
	p := Point{At: now, DiskBytes: s.DiskBytes}
	if !s.Running {
		st.last, st.players = server.Sample{}, nil
		st.latest = p
		return
	}
	var cpu float64
	var rx, tx int64
	if prev := st.last; prev.Running {
		if dt := s.Time.Sub(prev.Time); dt > 0 {
			if s.CPUNanos >= prev.CPUNanos {
				cpu = float64(s.CPUNanos-prev.CPUNanos) / float64(dt.Nanoseconds()) * 100
			}
			// Counters reset when the container restarts.
			rx, tx = counterDelta(prev.RxBytes, s.RxBytes), counterDelta(prev.TxBytes, s.TxBytes)
			p.RxBytes = int64(float64(rx) / dt.Seconds())
			p.TxBytes = int64(float64(tx) / dt.Seconds())
		}
	}
	st.last = s
	b.samples++
	b.cpuSum += cpu
	b.cpuMax = max(b.cpuMax, cpu)
	b.memSum += s.MemoryBytes
	b.memMax = max(b.memMax, s.MemoryBytes)
	b.rx += rx
	b.tx += tx
	p.Samples, p.CPUAvg, p.CPUMax, p.MemoryAvg, p.MemoryMax = 1, cpu, cpu, s.MemoryBytes, s.MemoryBytes
	if pl := st.players; pl != nil {
		b.playersSum += float64(pl.Online)
		b.playersN++
		b.playersMx = max(b.playersMx, pl.Online)
		avg, mx := float64(pl.Online), pl.Online
		p.PlayersAvg, p.PlayersMax = &avg, &mx
	}
	st.latest = p
}

func counterDelta(prev, cur uint64) int64 {
	if cur < prev {
		return int64(min(cur, 1<<62)) //nolint:gosec // bounded
	}
	return int64(min(cur-prev, 1<<62)) //nolint:gosec // bounded
}

func (c *Collector) write(ctx context.Context, id string, res int, b bucket) {
	if b.at.IsZero() {
		return
	}
	row := store.UpsertMetricParams{
		ServerID: id, Resolution: int64(res), At: b.at.UnixMilli(), Samples: int64(b.samples),
		CpuMax: b.cpuMax, MemoryMax: b.memMax, RxBytes: b.rx, TxBytes: b.tx, DiskBytes: b.disk,
	}
	if b.samples > 0 {
		row.CpuAvg = b.cpuSum / float64(b.samples)
		row.MemoryAvg = b.memSum / int64(b.samples)
	}
	if b.playersN > 0 {
		row.PlayersAvg = sql.NullFloat64{Float64: b.playersSum / float64(b.playersN), Valid: true}
		row.PlayersMax = sql.NullInt64{Int64: int64(b.playersMx), Valid: true}
	}
	if err := c.DB.Write.UpsertMetric(context.WithoutCancel(ctx), row); err != nil && ctx.Err() == nil {
		c.Log.Warn("writing metrics failed", "server", id, "err", err)
	}
}

// Flush writes every bucket in progress (on shutdown; a restart continues
// the same minute's row).
func (c *Collector) Flush(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, st := range c.state {
		c.write(ctx, id, Minute, st.bucket)
	}
}

// Rollup turns minute rows older than a day into 15-minute rows, and drops
// 15-minute rows older than a week.
func (c *Collector) Rollup(ctx context.Context) error {
	now := c.Now()
	cutoff := now.Add(-keepMinutes).Truncate(QuarterHour * time.Second)
	rows, err := c.DB.Write.MetricsBefore(ctx, cutoff.UnixMilli())
	if err != nil {
		return err
	}
	type key struct {
		server string
		at     int64
	}
	agg := map[key]*bucket{}
	var order []key
	for _, r := range rows {
		k := key{r.ServerID, time.UnixMilli(r.At).Truncate(QuarterHour * time.Second).UnixMilli()}
		b := agg[k]
		if b == nil {
			b = &bucket{at: time.UnixMilli(k.at)}
			agg[k] = b
			order = append(order, k)
		}
		n := int(r.Samples)
		b.samples += n
		b.cpuSum += r.CpuAvg * float64(n)
		b.cpuMax = max(b.cpuMax, r.CpuMax)
		b.memSum += r.MemoryAvg * int64(n)
		b.memMax = max(b.memMax, r.MemoryMax)
		b.rx += r.RxBytes
		b.tx += r.TxBytes
		b.disk = r.DiskBytes
		if r.PlayersAvg.Valid {
			b.playersSum += r.PlayersAvg.Float64
			b.playersN++
			b.playersMx = max(b.playersMx, int(r.PlayersMax.Int64))
		}
	}
	for _, k := range order {
		b := agg[k]
		// write averages players per minute row, so playersSum over
		// playersN minutes is right; CPU and memory are weighted by samples.
		c.write(ctx, k.server, QuarterHour, *b)
	}
	if _, err := c.DB.Write.DeleteMetricsBefore(ctx, store.DeleteMetricsBeforeParams{Resolution: Minute, At: cutoff.UnixMilli()}); err != nil {
		return err
	}
	_, err = c.DB.Write.DeleteMetricsBefore(ctx, store.DeleteMetricsBeforeParams{Resolution: QuarterHour, At: now.Add(-keepQuarters).UnixMilli()})
	return err
}

// Latest returns a server's most recent sample (zero if none yet).
func (c *Collector) Latest(id string) Point {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.state[id]; st != nil {
		return st.latest
	}
	return Point{}
}

// History returns a server's points since a time, oldest first: 15-minute
// points for what's older than a day, minute points after, and the minute
// in progress.
func (c *Collector) History(ctx context.Context, id string, since time.Time) ([]Point, error) {
	var out []Point
	for _, res := range []int{QuarterHour, Minute} {
		rows, err := c.DB.Read.ListMetrics(ctx, store.ListMetricsParams{ServerID: id, Resolution: int64(res), At: since.UnixMilli()})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, pointOf(r))
		}
	}
	c.mu.Lock()
	if st := c.state[id]; st != nil && !st.bucket.at.IsZero() && !st.bucket.at.Before(since) && c.Now().Sub(st.bucket.at) < 2*time.Minute {
		b := st.bucket
		r := store.Metric{
			At: b.at.UnixMilli(), Resolution: Minute, Samples: int64(b.samples), CpuMax: b.cpuMax,
			MemoryMax: b.memMax, RxBytes: b.rx, TxBytes: b.tx, DiskBytes: b.disk,
		}
		if b.samples > 0 {
			r.CpuAvg, r.MemoryAvg = b.cpuSum/float64(b.samples), b.memSum/int64(b.samples)
		}
		if b.playersN > 0 {
			r.PlayersAvg = sql.NullFloat64{Float64: b.playersSum / float64(b.playersN), Valid: true}
			r.PlayersMax = sql.NullInt64{Int64: int64(b.playersMx), Valid: true}
		}
		out = append(out, pointOf(r))
	}
	c.mu.Unlock()
	return out, nil
}

func pointOf(r store.Metric) Point {
	p := Point{
		At: time.UnixMilli(r.At), Resolution: int(r.Resolution), Samples: int(r.Samples),
		CPUAvg: r.CpuAvg, CPUMax: r.CpuMax, MemoryAvg: r.MemoryAvg, MemoryMax: r.MemoryMax,
		RxBytes: r.RxBytes, TxBytes: r.TxBytes, DiskBytes: r.DiskBytes,
	}
	if r.PlayersAvg.Valid {
		avg, mx := r.PlayersAvg.Float64, int(r.PlayersMax.Int64)
		p.PlayersAvg, p.PlayersMax = &avg, &mx
	}
	return p
}
