package thresholds

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

type fakeMetrics struct {
	mu      sync.Mutex
	servers map[string]metrics.Point
	host    metrics.HostPoint
}

func (f *fakeMetrics) Latest(id string) metrics.Point {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[id]
}

func (f *fakeMetrics) HostLatest() metrics.HostPoint {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.host
}

type fakeServers map[string]containers.Limits

func (f fakeServers) List() map[string]server.State {
	out := map[string]server.State{}
	for id := range f {
		out[id] = server.Running
	}
	return out
}

func (f fakeServers) Get(_ context.Context, id string) (*server.Server, error) {
	s := &server.Server{ID: id}
	s.Limits = f[id]
	return s, nil
}

func TestThresholds(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	out := events.New(db)
	now := time.Unix(1_700_000_000, 0)
	m := &fakeMetrics{servers: map[string]metrics.Point{}}
	servers := fakeServers{"a": {CPUPercent: 200, MemoryMiB: 1024, DiskMiB: 1024}, "b": {}}
	newEvaluator := func() *Evaluator {
		return &Evaluator{
			DB: db, Outbox: out, Metrics: m, Servers: servers, Now: func() time.Time { return now },
			Host: func() Host { return Host{CPUs: 4, MemoryBytes: 8 << 30, DiskBytes: 100 << 30} },
		}
	}
	e := newEvaluator()
	if err := e.Set(ctx, []Rule{{ID: "r", Condition: "bogus", Threshold: 50}}); err == nil {
		t.Error("an unknown condition was accepted")
	}
	if err := e.Set(ctx, []Rule{{ID: "r", Condition: "node_cpu_high", ServerID: "a", Threshold: 50}}); err == nil {
		t.Error("a node condition for a server was accepted")
	}
	rules := []Rule{
		{ID: "cpu", Condition: "cpu_high", Threshold: 90, ForSeconds: 60},
		{ID: "mem", Condition: "memory_high", ServerID: "a", Threshold: 80},
		{ID: "disk", Condition: "node_disk_high", Threshold: 90, ForSeconds: 0},
	}
	if err := e.Set(ctx, rules); err != nil {
		t.Fatal(err)
	}
	evs := func() []events.Event {
		t.Helper()
		list, err := out.Since(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	step := func(d time.Duration) {
		now = now.Add(d)
		e.Check(ctx)
	}
	set := func(id string, p metrics.Point) {
		m.mu.Lock()
		m.servers[id] = p
		m.mu.Unlock()
	}

	// a: 190% of one core is 95% of its 2-core limit; b (no limit): 380% is
	// 95% of the 4-core machine. Neither fires before a minute.
	set("a", metrics.Point{CPUAvg: 190, MemoryAvg: 512 << 20})
	set("b", metrics.Point{CPUAvg: 380})
	step(0)
	step(30 * time.Second)
	if n := len(evs()); n != 0 {
		t.Fatalf("%d events before the rule held a minute", n)
	}
	step(30 * time.Second)
	got := evs()
	if len(got) != 2 || got[0].Type != "alert.fired" || got[0].Data["rule_id"] != "cpu" {
		t.Fatalf("after a minute: %+v", got)
	}
	step(10 * time.Second)
	if len(evs()) != 2 {
		t.Fatal("fired twice")
	}

	// A restart remembers what's firing.
	e = newEvaluator()
	if err := e.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Rules()) != 3 {
		t.Fatalf("rules after a restart: %v", e.Rules())
	}
	step(10 * time.Second)
	if len(evs()) != 2 {
		t.Fatal("fired again after a restart")
	}

	// Under for a minute resolves; a dip shorter than that doesn't.
	set("a", metrics.Point{CPUAvg: 20})
	step(30 * time.Second)
	set("a", metrics.Point{CPUAvg: 190})
	step(10 * time.Second)
	set("a", metrics.Point{CPUAvg: 20})
	step(10 * time.Second)
	step(30 * time.Second)
	if len(evs()) != 2 {
		t.Fatalf("resolved too soon: %+v", evs())
	}
	step(30 * time.Second)
	got = evs()
	if len(got) != 3 || got[2].Type != "alert.resolved" || got[2].ServerID != "a" {
		t.Fatalf("resolved: %+v", got)
	}

	// Memory over its limit's 80%, at once; the machine's disk.
	set("a", metrics.Point{CPUAvg: 20, MemoryAvg: 900 << 20})
	m.mu.Lock()
	m.host.DiskBytes = 95 << 30
	m.mu.Unlock()
	step(10 * time.Second)
	got = evs()
	if len(got) != 5 {
		t.Fatalf("memory and disk: %+v", got)
	}

	// Replacing the rules forgets what a removed or changed rule had.
	if err := e.Set(ctx, rules[:1]); err != nil {
		t.Fatal(err)
	}
	step(10 * time.Second)
	if len(evs()) != 5 {
		t.Fatal("a removed rule still raised events")
	}
}
