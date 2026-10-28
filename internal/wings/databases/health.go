package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Health (docs/DATABASES.md#health-and-alerts): every watchEvery, each
// instance that should be running is looked at. One Docker restarted, or
// that stopped, is down until it answers again: that's an event
// (db.instance.down, then db.instance.up), once however often it restarts,
// so the Panel can alert on it. Its usage (memory, disk, connections,
// uptime, and queries a second) is sampled too, for the app (node.db.usage),
// raptor db ps, and the threshold alerts on its disk and connections.

// Events about instances. They have no server; their data names the
// instance (instance_id, instance, engine, version).
const (
	EventDown    = "db.instance.down"    // error: why
	EventUp      = "db.instance.up"      // down_seconds
	EventVersion = "db.instance.version" // kind, from, to, ok, error
)

const (
	watchEvery = 15 * time.Second
	// softDiskEvery: without quotas, an instance's disk use is a scan of
	// its folder, so it isn't measured every time.
	softDiskEvery = 5 * time.Minute
	// downKey keeps which instances are down across Wings restarts, so a
	// restart neither repeats an alert nor misses the instance coming back.
	downKey = "databases.down"
	// usageHistory is how many samples each instance keeps for its trend:
	// 10 minutes of them.
	usageHistory = 40
)

// Usage is an instance's latest sample.
type Usage struct {
	At               time.Time `json:"at"`
	MemoryBytes      int64     `json:"memory_bytes"`
	MemoryLimitBytes int64     `json:"memory_limit_bytes"`
	DiskBytes        int64     `json:"disk_bytes"`
	DiskLimitBytes   int64     `json:"disk_limit_bytes"` // 0 = none
	Connections      int       `json:"connections"`
	MaxConnections   int       `json:"max_connections"`
	// UptimeSeconds is how long the engine has been up, by its own count.
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
	// QueriesPerSecond is the engine's rate since the sample before:
	// statements (MySQL, MariaDB), transactions (PostgreSQL), commands
	// (Redis, Valkey), or operations (MongoDB), less Wings' own sampling.
	// nil until two samples of the same run.
	QueriesPerSecond *float64 `json:"queries_per_second,omitempty"`

	diskAt time.Time
	ops    int64     // the engine's counter behind QueriesPerSecond
	opsAt  time.Time // when it was read
}

// UsagePoint is one of an instance's recent samples, for its trend.
type UsagePoint struct {
	At               time.Time `json:"at"`
	MemoryBytes      int64     `json:"memory_bytes"`
	Connections      int       `json:"connections"`
	QueriesPerSecond *float64  `json:"queries_per_second,omitempty"`
}

// health is what the watch keeps.
type health struct {
	once     sync.Once
	usage    map[string]Usage
	recent   map[string][]UsagePoint // the last usageHistory samples, oldest first
	restarts map[string]int          // Docker's restart count when it last started, by instance
	down     map[string]int64        // since (unix seconds), by instance
	// restart starts an instance found down again (ensure; tests replace
	// it).
	restart func(ctx context.Context, id string)
}

func (s *Service) initHealth() {
	s.health.once.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.health.usage = map[string]Usage{}
		s.health.recent = map[string][]UsagePoint{}
		s.health.restarts = map[string]int{}
		s.health.down = map[string]int64{}
		if s.health.restart == nil {
			s.health.restart = func(ctx context.Context, id string) { go s.ensure(ctx, id) }
		}
	})
}

// loadDown reads which instances were down when Wings stopped.
func (s *Service) loadDown(ctx context.Context) {
	s.initHealth()
	raw, err := s.o.Store.Read.GetKV(ctx, downKey)
	if err != nil {
		return
	}
	var down map[string]int64
	if json.Unmarshal(raw, &down) != nil {
		return
	}
	s.mu.Lock()
	maps.Copy(s.health.down, down)
	s.mu.Unlock()
}

func (s *Service) saveDown(ctx context.Context) {
	s.mu.Lock()
	raw, _ := json.Marshal(s.health.down)
	s.mu.Unlock()
	if err := s.o.Store.Write.SetKV(context.WithoutCancel(ctx), store.SetKVParams{Key: downKey, Value: raw}); err != nil {
		s.o.Log.Error("recording a database instance down failed", "err", err)
	}
}

// event records an event about an instance.
func (s *Service) event(ctx context.Context, typ string, in Instance, data map[string]any) {
	if s.o.Events == nil {
		return
	}
	data["instance_id"], data["instance"], data["engine"], data["version"] = in.ID, in.Name, in.Engine, in.Version
	if _, err := s.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: typ, Data: data}); err != nil {
		s.o.Log.Error("recording a database instance event failed", "type", typ, "instance", in.ID, "err", err)
	}
}

// markDown records an instance that should be running isn't, once until
// it's back.
func (s *Service) markDown(ctx context.Context, id, why string) {
	s.initHealth()
	in, _, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil || in.DesiredState != StateRunning {
		return
	}
	s.mu.Lock()
	_, already := s.health.down[id]
	if !already {
		s.health.down[id] = time.Now().Unix()
	}
	s.mu.Unlock()
	if already {
		return
	}
	s.saveDown(ctx)
	s.event(ctx, EventDown, in, map[string]any{"error": why})
}

// markUp records an instance that was down answers again.
func (s *Service) markUp(ctx context.Context, id string) {
	s.initHealth()
	s.mu.Lock()
	since, was := s.health.down[id]
	delete(s.health.down, id)
	s.mu.Unlock()
	if !was {
		return
	}
	s.saveDown(ctx)
	if in, _, err := s.instance(ctx, s.o.Store.Read, id); err == nil {
		s.event(ctx, EventUp, in, map[string]any{"down_seconds": max(time.Now().Unix()-since, 0)})
	}
}

// forget drops what the watch knows of an instance that was stopped or
// deleted: one that was down isn't any more, without an alert saying so.
func (s *Service) forget(ctx context.Context, id string) {
	s.initHealth()
	s.mu.Lock()
	_, was := s.health.down[id]
	delete(s.health.down, id)
	delete(s.health.usage, id)
	delete(s.health.recent, id)
	delete(s.health.restarts, id)
	s.mu.Unlock()
	if was {
		s.saveDown(ctx)
	}
}

// started records Docker's restart count once an instance answers, so the
// watch sees Docker restarting it.
func (s *Service) started(ctx context.Context, id string) {
	s.initHealth()
	st, ok, err := s.o.Runtime.ServiceState(ctx, ContainerName(id))
	if err != nil || !ok {
		return
	}
	s.mu.Lock()
	s.health.restarts[id] = st.Restarts
	s.mu.Unlock()
}

// tryLock takes an instance's lock if nothing holds it.
func (s *Service) tryLock(id string) (func(), bool) {
	s.mu.Lock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	s.mu.Unlock()
	if !l.TryLock() {
		return nil, false
	}
	return l.Unlock, true
}

func (s *Service) watch(ctx context.Context) {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkAll(ctx)
		}
	}
}

// checkAll looks at every instance once.
func (s *Service) checkAll(ctx context.Context) {
	s.initHealth()
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return
		}
		s.checkOne(ctx, r.ID)
	}
}

// checkOne looks at one instance that's running: if it stopped, or Docker
// restarted it, it's down and started again (ensure waits for it to answer,
// and says it failed if it keeps stopping); otherwise its usage is sampled.
// One being started, changed, or stopped (its lock held) is left alone.
func (s *Service) checkOne(ctx context.Context, id string) {
	unlock, ok := s.tryLock(id)
	if !ok {
		return
	}
	defer unlock()
	if s.state(id) != StateRunning {
		return
	}
	in, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil || in.DesiredState != StateRunning {
		return
	}
	st, found, err := s.o.Runtime.ServiceState(ctx, ContainerName(id))
	if err != nil {
		return // Docker isn't answering: nothing to say about the instance
	}
	s.mu.Lock()
	base, known := s.health.restarts[id]
	s.mu.Unlock()
	why := ""
	switch {
	case !found:
		why = "its container is gone"
	case !st.Running:
		why = fmt.Sprintf("it stopped (exit code %d)%s", st.ExitCode, oom(st.OOMKilled))
	case known && st.Restarts > base:
		why = "it stopped and Docker restarted it" + oom(st.OOMKilled)
	}
	if why != "" {
		s.o.Log.Warn("database instance down; starting it again", "instance", id, "why", why)
		s.markDown(ctx, id, why)
		s.setStatus(id, StateStarting, nil)
		s.health.restart(context.WithoutCancel(ctx), id)
		return
	}
	s.sample(ctx, in, r)
}

// sample measures a running instance's usage.
func (s *Service) sample(ctx context.Context, in Instance, r store.DatabaseInstance) {
	now := time.Now()
	s.mu.Lock()
	prev := s.health.usage[in.ID]
	s.mu.Unlock()
	u := Usage{
		At: now, MemoryLimitBytes: in.Limits.MemoryMiB << 20, DiskLimitBytes: in.Limits.DiskMiB << 20,
		MaxConnections: maxConnections(in), DiskBytes: prev.DiskBytes, diskAt: prev.diskAt, Connections: prev.Connections,
		UptimeSeconds: prev.UptimeSeconds, ops: prev.ops, opsAt: prev.opsAt,
	}
	if st, err := s.o.Runtime.Stats(ctx, ContainerName(in.ID)); err == nil {
		u.MemoryBytes = st.MemoryBytes
	}
	dir := s.dir(in.ID)
	switch {
	case s.o.Volume != nil && !s.o.Volume.Soft:
		if d, err := s.o.Volume.Usage(ctx, dir, uint32(r.QuotaProject.Int64)); err == nil { //nolint:gosec // allocated by Wings from 1000 up
			u.DiskBytes, u.diskAt = d.Bytes, now
		}
	case now.Sub(prev.diskAt) >= softDiskEvery:
		if d, err := storage.Scan(ctx, dir); err == nil {
			u.DiskBytes, u.diskAt = d.Bytes, now
		}
	}
	if root, err := s.sealer.open(r.RootPassword); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if st, err := readStatus(cctx, in, dir, root); err == nil {
			u.Connections, u.UptimeSeconds = st.connections, st.uptime
			u.QueriesPerSecond = queryRate(prev, st, now, ownOps[in.Engine])
			u.ops, u.opsAt = st.ops, now
		}
		cancel()
	}
	s.mu.Lock()
	s.health.usage[in.ID] = u
	recent := append(s.health.recent[in.ID], UsagePoint{
		At: now, MemoryBytes: u.MemoryBytes, Connections: u.Connections, QueriesPerSecond: u.QueriesPerSecond,
	})
	s.health.recent[in.ID] = recent[max(len(recent)-usageHistory, 0):]
	s.mu.Unlock()
}

func maxConnections(in Instance) int {
	if in.Settings.MaxConnections > 0 {
		return in.Settings.MaxConnections
	}
	return defaultMaxConnections
}

// engineStatus is what an engine says about itself now.
type engineStatus struct {
	connections int   // clients connected, Wings' own asking included
	uptime      int64 // seconds since it started
	ops         int64 // its counter of what clients asked since it started
}

// ownOps is how much each sample adds to an engine's counter itself (its
// connection's setup, the asking, and closing it), measured against real
// engines (TestEngines), so an idle instance reads 0 queries a second.
var ownOps = map[string]int64{MariaDB: 3, MySQL: 3, Postgres: 2, Redis: 2, Valkey: 2, MongoDB: 7}

// queryRate is an engine's rate between two reads of its counter, less
// what sampling it added; nil without an earlier read from the same run
// (it restarted, or its counters were reset).
func queryRate(prev Usage, st engineStatus, at time.Time, own int64) *float64 {
	secs := at.Sub(prev.opsAt).Seconds()
	if prev.opsAt.IsZero() || secs <= 0 || st.ops < prev.ops || st.uptime < prev.UptimeSeconds {
		return nil
	}
	q := float64(max(st.ops-prev.ops-own, 0)) / secs
	return &q
}

// readStatus asks an instance for its connections, uptime, and counter,
// in one statement or command on one connection, so what sampling adds to
// the counter stays the same (ownOps).
func readStatus(ctx context.Context, in Instance, dir, root string) (engineStatus, error) {
	switch in.Engine {
	case Redis, Valkey:
		c := redisClient(dir, root)
		defer func() { _ = c.Close() }()
		info, err := c.Info(ctx).Result()
		if err != nil {
			return engineStatus{}, err
		}
		n, err1 := infoField(info, "connected_clients")
		up, err2 := infoField(info, "uptime_in_seconds")
		ops, err3 := infoField(info, "total_commands_processed")
		return engineStatus{connections: n, uptime: int64(up), ops: int64(ops)}, errors.Join(err1, err2, err3)
	case MongoDB:
		c, err := mongoConnect(ctx, dir, root)
		if err != nil {
			return engineStatus{}, err
		}
		defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
		var res struct {
			Uptime      float64 `bson:"uptime"`
			Connections struct {
				Current int `bson:"current"`
			} `bson:"connections"`
			Ops struct {
				Insert  int64 `bson:"insert"`
				Query   int64 `bson:"query"`
				Update  int64 `bson:"update"`
				Delete  int64 `bson:"delete"`
				Getmore int64 `bson:"getmore"`
				Command int64 `bson:"command"`
			} `bson:"opcounters"`
		}
		cmd := bson.D{{Key: "serverStatus", Value: 1}, {Key: "repl", Value: 0}, {Key: "metrics", Value: 0}, {Key: "locks", Value: 0}}
		if err := c.Database("admin").RunCommand(ctx, cmd).Decode(&res); err != nil {
			return engineStatus{}, err
		}
		o := res.Ops
		ops := o.Insert + o.Query + o.Update + o.Delete + o.Getmore + o.Command
		return engineStatus{connections: res.Connections.Current, uptime: int64(res.Uptime), ops: ops}, nil
	}
	db, err := connect(in, dir, root)
	if err != nil {
		return engineStatus{}, err
	}
	defer func() { _ = db.Close() }()
	var st engineStatus
	if in.Engine == Postgres {
		err = db.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'),
			extract(epoch FROM now() - pg_postmaster_start_time())::bigint,
			(SELECT coalesce(sum(xact_commit + xact_rollback), 0) FROM pg_stat_database)::bigint`).Scan(&st.connections, &st.uptime, &st.ops)
		if errors.Is(err, sql.ErrNoRows) {
			return engineStatus{}, errors.New("no status")
		}
		return st, err
	}
	rows, err := db.QueryContext(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_connected', 'Uptime', 'Questions')")
	if err != nil {
		return engineStatus{}, err
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return engineStatus{}, err
		}
		switch strings.ToLower(name) {
		case "threads_connected":
			st.connections = int(n)
		case "uptime":
			st.uptime = n
		case "questions":
			st.ops = n
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return engineStatus{}, err
	}
	if seen != 3 {
		return engineStatus{}, errors.New("no status")
	}
	return st, nil
}

// infoField reads a number from Redis' INFO.
func infoField(info, field string) (int, error) {
	for line := range strings.SplitSeq(info, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), field+":"); ok {
			return strconv.Atoi(v)
		}
	}
	return 0, fmt.Errorf("no %s in INFO", field)
}

// Usage returns an instance's latest sample; ok is false if there's none
// yet.
func (s *Service) Usage(id string) (Usage, bool) {
	s.initHealth()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.health.usage[id]
	return u, ok
}

// Recent is an instance's last samples, oldest first.
func (s *Service) Recent(id string) []UsagePoint {
	s.initHealth()
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.health.recent[id])
}

// Usages are the latest samples of the instances running now, by ID.
func (s *Service) Usages() map[string]Usage {
	s.initHealth()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Usage, len(s.health.usage))
	for id, u := range s.health.usage {
		if s.status[id].state == StateRunning {
			out[id] = u
		}
	}
	return out
}

// NetworkCheck is an instance on one of its networks, as Docker has it.
type NetworkCheck struct {
	ID       string `json:"id"` // "" for the server network
	Docker   string `json:"docker"`
	Address  string `json:"address,omitempty"` // its fixed address, if it has one
	Attached bool   `json:"attached"`
	// Actual is its address there now, if it's attached.
	Actual string `json:"actual,omitempty"`
}

// Check is an instance's health, checked now (raptor doctor).
type Check struct {
	// Answering: it answered on its socket as the real server.
	Answering bool           `json:"answering"`
	Error     string         `json:"error,omitempty"`
	Networks  []NetworkCheck `json:"networks"`
}

// Check asks an instance to answer on its socket now, and checks it's on
// each of its networks at its address. It changes nothing.
func (s *Service) Check(ctx context.Context, id string) (Check, error) {
	in, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return Check{}, err
	}
	var c Check
	root, err := s.sealer.open(r.RootPassword)
	if err != nil {
		return Check{}, err
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = ping(pctx, in, s.dir(id), root)
	cancel()
	c.Answering = err == nil
	if err != nil {
		c.Error = err.Error()
	}
	addrs, err := s.o.Store.Read.ListInstanceAddresses(ctx)
	if err != nil {
		return Check{}, err
	}
	want := map[string]NetworkCheck{}
	for _, nid := range in.Networks {
		n := NetworkCheck{ID: nid, Docker: networks.DockerName(nid)}
		for _, a := range addrs {
			if a.InstanceID == id && a.NetworkID == nid {
				n.Address = a.Ip
			}
		}
		want[n.Docker] = n
	}
	if len(in.Networks) == 0 && s.o.ServerNetwork.Name != "" {
		want[s.o.ServerNetwork.Name] = NetworkCheck{Docker: s.o.ServerNetwork.Name}
	}
	st, found, err := s.o.Runtime.ServiceState(ctx, ContainerName(id))
	if err != nil {
		return Check{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		n := want[name]
		if a, ok := st.Networks[name]; found && ok {
			n.Attached = true
			if a.IsValid() {
				n.Actual = a.String()
			}
		}
		c.Networks = append(c.Networks, n)
	}
	return c, nil
}

// Problem is what's wrong with an instance's networks, for people: "" if
// nothing is.
func (c Check) Problem() string {
	var bad []string
	for _, n := range c.Networks {
		switch {
		case !n.Attached:
			bad = append(bad, "not on "+n.Docker)
		case n.Address != "" && n.Actual != n.Address:
			bad = append(bad, fmt.Sprintf("on %s at %s instead of %s", n.Docker, n.Actual, n.Address))
		}
	}
	return strings.Join(bad, "; ")
}
