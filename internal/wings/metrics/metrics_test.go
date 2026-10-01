package metrics

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// fakeServers serves samples from counters the test moves forward.
type fakeServers struct {
	mu      sync.Mutex
	now     *time.Time
	running map[string]bool
	cpu     map[string]uint64
	rx      map[string]uint64
	mem     int64
	disk    int64
}

func (f *fakeServers) List() map[string]server.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]server.State{}
	for id, up := range f.running {
		out[id] = map[bool]server.State{true: server.Running, false: server.Offline}[up]
	}
	return out
}

func (f *fakeServers) Sample(_ context.Context, id string) (server.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := server.Sample{At: *f.now, DiskBytes: f.disk, State: server.Offline}
	if f.running[id] {
		s.State, s.Running = server.Running, true
		s.Stats = containers.Stats{Time: *f.now, CPUNanos: f.cpu[id], MemoryBytes: f.mem, RxBytes: f.rx[id]}
		s.Query, s.QueryAddr = "minecraft", "127.0.0.1:1"
	}
	return s, nil
}

type env struct {
	t   *testing.T
	now time.Time
	srv *fakeServers
	c   *Collector
	db  *store.DB
}

func newEnv(t *testing.T) *env {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"a", "b"} {
		if err := db.Write.InsertServer(context.Background(), store.InsertServerParams{
			ID: id, Name: id, Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
			Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "running", InstallState: "installed",
		}); err != nil {
			t.Fatal(err)
		}
	}
	e := &env{t: t, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), db: db}
	e.srv = &fakeServers{now: &e.now, running: map[string]bool{"a": true, "b": false}, cpu: map[string]uint64{}, rx: map[string]uint64{}, mem: 1 << 30, disk: 5 << 30}
	players := 4
	e.c = &Collector{
		Servers: e.srv, DB: db, Now: func() time.Time { return e.now },
		Query: func(context.Context, string, string) (Players, error) {
			players++
			return Players{Online: players, Max: 20}, nil
		},
	}
	return e
}

// step moves 10 seconds on, with server a using half a core and 1 MB/s in.
func (e *env) step() {
	e.srv.mu.Lock()
	e.now = e.now.Add(SampleEvery)
	e.srv.cpu["a"] += uint64(SampleEvery.Nanoseconds() / 2)
	e.srv.rx["a"] += 10 << 20
	e.srv.mu.Unlock()
	e.c.Tick(context.Background())
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	start := e.now
	e.c.Tick(ctx)  // the first sample has no rates yet
	for range 12 { // two minutes
		e.step()
	}
	l := e.c.Latest("a")
	if l.CPUAvg < 49 || l.CPUAvg > 51 || l.RxBytes != 1<<20 || l.MemoryAvg != 1<<30 || l.PlayersAvg == nil {
		t.Fatalf("latest: %+v", l)
	}
	if l := e.c.Latest("b"); l.Samples != 0 || l.DiskBytes != 5<<30 {
		t.Errorf("stopped server: %+v", l)
	}

	h, err := e.c.History(ctx, "a", start)
	if err != nil {
		t.Fatal(err)
	}
	// 12:00 and 12:01 written, 12:02 in progress.
	if len(h) != 3 || h[0].Resolution != Minute || !h[1].At.Equal(start.Add(time.Minute)) {
		t.Fatalf("history: %+v", h)
	}
	m := h[1]
	if m.Samples != 6 || m.CPUAvg < 49 || m.CPUAvg > 51 || m.RxBytes != 60<<20 || m.DiskBytes != 5<<30 || m.PlayersMax == nil {
		t.Errorf("12:01: %+v", m)
	}
	if hb, _ := e.c.History(ctx, "b", start); len(hb) != 3 || hb[1].Samples != 0 || hb[1].PlayersAvg != nil {
		t.Errorf("stopped server's history: %+v", hb)
	}

	// A restart of the container resets its counters: no negative traffic.
	e.srv.mu.Lock()
	e.srv.rx["a"] = 0
	e.srv.mu.Unlock()
	e.step()
	if l := e.c.Latest("a"); l.RxBytes < 0 {
		t.Errorf("after a restart: %+v", l)
	}

	// A deleted server is forgotten.
	e.srv.mu.Lock()
	delete(e.srv.running, "b")
	e.srv.mu.Unlock()
	e.step()
	if l := e.c.Latest("b"); !l.At.IsZero() {
		t.Errorf("deleted server: %+v", l)
	}
}

// Minute rows older than a day become 15-minute rows; those go after a week.
func TestRollup(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	start := e.now
	e.c.Tick(ctx)
	for range 6 * 30 { // 30 minutes
		e.step()
	}
	e.c.Flush(ctx)
	e.now = start.Add(25 * time.Hour)
	if err := e.c.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := e.c.History(ctx, "a", start)
	if err != nil {
		t.Fatal(err)
	}
	var quarters []Point
	for _, p := range h {
		if p.Resolution == Minute {
			t.Fatalf("a minute row older than a day is left: %+v", p)
		}
		quarters = append(quarters, p)
	}
	if len(quarters) != 3 {
		t.Fatalf("%d quarter-hour rows: %+v", len(quarters), quarters)
	}
	q := quarters[0]
	if q.Samples != 6*15 || q.CPUAvg < 49 || q.CPUAvg > 51 || q.RxBytes < 14*60<<20 || q.PlayersMax == nil {
		t.Errorf("first quarter: %+v", q)
	}
	e.now = start.Add(8 * 24 * time.Hour)
	if err := e.c.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _ := e.c.History(ctx, "a", start); len(h) != 0 {
		t.Errorf("after a week: %d rows", len(h))
	}
}

// A game that doesn't answer has no player count, rather than a stale one.
func TestPlayersUnknown(t *testing.T) {
	e := newEnv(t)
	e.c.Query = func(context.Context, string, string) (Players, error) { return Players{}, errors.New("timeout") }
	e.c.Tick(context.Background())
	e.step()
	if l := e.c.Latest("a"); l.PlayersAvg != nil {
		t.Errorf("players: %v", *l.PlayersAvg)
	}
}
