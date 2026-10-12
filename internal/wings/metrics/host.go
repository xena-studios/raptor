package metrics

import (
	"context"
	"time"

	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// The machine's own history, beside its servers': its CPU, memory, disk,
// network, and load, kept the same way (docs/WINGS.md#local-metrics).

// HostPoint is the machine's resources over one bucket (or, for HostLatest,
// right now).
type HostPoint struct {
	At         time.Time
	Resolution int // seconds; 0 for a live sample
	Samples    int
	CPUAvg     float64 // percent of the whole machine
	CPUMax     float64
	MemoryAvg  int64
	MemoryMax  int64
	RxBytes    int64 // traffic in the bucket; for a live sample, per second
	TxBytes    int64
	DiskBytes  int64
	Load       float64
}

type hostState struct {
	last   host.Sample
	bucket hostBucket
	latest HostPoint
}

type hostBucket struct {
	at             time.Time
	samples        int
	cpuSum, cpuMax float64
	memSum, memMax int64
	rx, tx         int64
	disk           int64
	loadSum        float64
}

func (c *Collector) sampleHost(ctx context.Context) {
	if c.Host == nil {
		return
	}
	s := c.Host()
	var disk int64
	if c.HostDisk != nil {
		disk = c.HostDisk()
	}
	now := c.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	st := &c.host
	p := HostPoint{At: now, MemoryAvg: s.MemoryUsed, MemoryMax: s.MemoryUsed, DiskBytes: disk, Load: s.Load1}
	prev := st.last
	st.last = s
	if prev.Time.IsZero() || s.CPUTotal == 0 {
		// Rates need two samples: the first only says what's used now.
		st.latest = p
		return
	}
	cpu := host.CPUPercent(prev, s)
	rx, tx := counterDelta(prev.RxBytes, s.RxBytes), counterDelta(prev.TxBytes, s.TxBytes)
	if dt := s.Time.Sub(prev.Time).Seconds(); dt > 0 {
		p.RxBytes, p.TxBytes = int64(float64(rx)/dt), int64(float64(tx)/dt)
	}
	p.Samples, p.CPUAvg, p.CPUMax = 1, cpu, cpu
	st.latest = p

	start := now.Truncate(time.Minute)
	if !st.bucket.at.IsZero() && !st.bucket.at.Equal(start) {
		c.writeHost(ctx, Minute, st.bucket)
		st.bucket = hostBucket{}
	}
	b := &st.bucket
	b.at, b.disk = start, disk
	b.samples++
	b.cpuSum += cpu
	b.cpuMax = max(b.cpuMax, cpu)
	b.memSum += s.MemoryUsed
	b.memMax = max(b.memMax, s.MemoryUsed)
	b.rx += rx
	b.tx += tx
	b.loadSum += s.Load1
}

func (c *Collector) writeHost(ctx context.Context, res int, b hostBucket) {
	if b.at.IsZero() || b.samples == 0 {
		return
	}
	n := float64(b.samples)
	row := store.UpsertHostMetricParams{
		Resolution: int64(res), At: b.at.UnixMilli(), Samples: int64(b.samples),
		CpuAvg: b.cpuSum / n, CpuMax: b.cpuMax, MemoryAvg: b.memSum / int64(b.samples), MemoryMax: b.memMax,
		RxBytes: b.rx, TxBytes: b.tx, DiskBytes: b.disk, LoadAvg: b.loadSum / n,
	}
	if err := c.DB.Write.UpsertHostMetric(context.WithoutCancel(ctx), row); err != nil && ctx.Err() == nil {
		c.Log.Warn("writing host metrics failed", "err", err)
	}
}

// rollupHost turns the machine's minute rows before cutoff into 15-minute
// rows, and drops 15-minute rows older than a week.
func (c *Collector) rollupHost(ctx context.Context, cutoff time.Time) error {
	rows, err := c.DB.Write.HostMetricsBefore(ctx, cutoff.UnixMilli())
	if err != nil {
		return err
	}
	agg := map[int64]*hostBucket{}
	var order []int64
	for _, r := range rows {
		k := time.UnixMilli(r.At).Truncate(QuarterHour * time.Second).UnixMilli()
		b := agg[k]
		if b == nil {
			b = &hostBucket{at: time.UnixMilli(k)}
			agg[k] = b
			order = append(order, k)
		}
		n := r.Samples
		b.samples += int(n)
		b.cpuSum += r.CpuAvg * float64(n)
		b.cpuMax = max(b.cpuMax, r.CpuMax)
		b.memSum += r.MemoryAvg * n
		b.memMax = max(b.memMax, r.MemoryMax)
		b.rx += r.RxBytes
		b.tx += r.TxBytes
		b.disk = r.DiskBytes
		b.loadSum += r.LoadAvg * float64(n)
	}
	for _, k := range order {
		c.writeHost(ctx, QuarterHour, *agg[k])
	}
	if _, err := c.DB.Write.DeleteHostMetricsBefore(ctx, store.DeleteHostMetricsBeforeParams{Resolution: Minute, At: cutoff.UnixMilli()}); err != nil {
		return err
	}
	_, err = c.DB.Write.DeleteHostMetricsBefore(ctx, store.DeleteHostMetricsBeforeParams{Resolution: QuarterHour, At: c.Now().Add(-keepQuarters).UnixMilli()})
	return err
}

// HostLatest returns the machine's most recent sample (zero if none yet).
func (c *Collector) HostLatest() HostPoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.host.latest
}

// HostHistory returns the machine's points since a time, oldest first, like
// History.
func (c *Collector) HostHistory(ctx context.Context, since time.Time) ([]HostPoint, error) {
	var out []HostPoint
	for _, res := range []int{QuarterHour, Minute} {
		rows, err := c.DB.Read.ListHostMetrics(ctx, store.ListHostMetricsParams{Resolution: int64(res), At: since.UnixMilli()})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, hostPointOf(r))
		}
	}
	c.mu.Lock()
	if b := c.host.bucket; !b.at.IsZero() && b.samples > 0 && !b.at.Before(since) && c.Now().Sub(b.at) < 2*time.Minute {
		n := float64(b.samples)
		out = append(out, HostPoint{
			At: b.at, Resolution: Minute, Samples: b.samples, CPUAvg: b.cpuSum / n, CPUMax: b.cpuMax,
			MemoryAvg: b.memSum / int64(b.samples), MemoryMax: b.memMax, RxBytes: b.rx, TxBytes: b.tx,
			DiskBytes: b.disk, Load: b.loadSum / n,
		})
	}
	c.mu.Unlock()
	return out, nil
}

func hostPointOf(r store.HostMetric) HostPoint {
	return HostPoint{
		At: time.UnixMilli(r.At), Resolution: int(r.Resolution), Samples: int(r.Samples),
		CPUAvg: r.CpuAvg, CPUMax: r.CpuMax, MemoryAvg: r.MemoryAvg, MemoryMax: r.MemoryMax,
		RxBytes: r.RxBytes, TxBytes: r.TxBytes, DiskBytes: r.DiskBytes, Load: r.LoadAvg,
	}
}
