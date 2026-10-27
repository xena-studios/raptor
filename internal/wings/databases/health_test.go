package databases

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// fakeRuntime is Docker, as far as the watch looks.
type fakeRuntime struct {
	mu    sync.Mutex
	state containers.State
	found bool
}

func (f *fakeRuntime) EnsureService(context.Context, containers.ServiceSpec) error { return nil }
func (f *fakeRuntime) StopService(context.Context, string, time.Duration) error    { return nil }
func (f *fakeRuntime) RemoveService(context.Context, string) error                 { return nil }
func (f *fakeRuntime) Exec(context.Context, string, containers.ExecSpec) (int, error) {
	return 0, nil
}
func (f *fakeRuntime) ServiceLogs(context.Context, string, int) (string, error) { return "", nil }
func (f *fakeRuntime) Stats(context.Context, string) (containers.Stats, error) {
	return containers.Stats{MemoryBytes: 300 << 20}, nil
}

func (f *fakeRuntime) ServiceState(context.Context, string) (containers.State, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.found, nil
}

func (f *fakeRuntime) set(st containers.State, found bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state, f.found = st, found
}

type healthTest struct {
	svc       *Service
	db        *store.DB
	outbox    *events.Outbox
	rt        *fakeRuntime
	restarted []string
	seen      int64
}

const testInstance = "0199e2e0-0000-7000-8000-0000000000d1"

func newHealthTest(t *testing.T) *healthTest {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &healthTest{db: db, outbox: events.New(db), rt: &fakeRuntime{state: containers.State{Running: true}, found: true}}
	h.svc = h.open(t, dir)
	root, err := h.svc.sealer.seal("hunter2hunter2hunter2hunter2abcd")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write.InsertInstance(ctx, store.InsertInstanceParams{
		ID: testInstance, Name: "main", Engine: MariaDB, Version: "11.8", Image: "mariadb:11.8", RootPassword: root,
		Networks: "[]", Limits: `{"memory_mib":1024,"disk_mib":2048}`, Settings: `{"max_connections":50}`, DesiredState: StateRunning,
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.setStatus(testInstance, StateRunning, nil)
	h.svc.started(ctx, testInstance)
	return h
}

// open makes a Service on the test's state, as a Wings start would.
func (h *healthTest) open(t *testing.T, dir string) *Service {
	t.Helper()
	svc, err := New(Options{
		Store: h.db, Runtime: h.rt, Log: slog.New(slog.DiscardHandler), Dir: filepath.Join(dir, "databases"),
		KeyPath: filepath.Join(dir, "databases.key"), Events: h.outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.health.restart = func(_ context.Context, id string) { h.restarted = append(h.restarted, id) }
	svc.loadDown(context.Background())
	return svc
}

// events returns the events since the last call.
func (h *healthTest) events(t *testing.T) []events.Event {
	t.Helper()
	evs, err := h.outbox.Since(context.Background(), h.seen, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) > 0 {
		h.seen = evs[len(evs)-1].Seq
	}
	return evs
}

func TestWatch(t *testing.T) {
	ctx := context.Background()
	h := newHealthTest(t)

	// Running and answering: sampled, nothing to say.
	h.svc.checkAll(ctx)
	if evs := h.events(t); len(evs) != 0 {
		t.Fatalf("events while it runs: %+v", evs)
	}
	u, ok := h.svc.Usage(testInstance)
	if !ok || u.MemoryBytes != 300<<20 || u.MemoryLimitBytes != 1<<30 || u.DiskLimitBytes != 2<<30 || u.MaxConnections != 50 {
		t.Errorf("usage %+v %v", u, ok)
	}
	if _, ok := h.svc.Usages()[testInstance]; !ok {
		t.Error("a running instance's usage isn't in Usages")
	}

	// Docker restarted it: down once, and started again.
	h.rt.set(containers.State{Running: true, Restarts: 1}, true)
	h.svc.checkAll(ctx)
	evs := h.events(t)
	if len(evs) != 1 || evs[0].Type != EventDown || evs[0].ServerID != "" || evs[0].Data["instance_id"] != testInstance ||
		evs[0].Data["instance"] != "main" || evs[0].Data["error"] != "it stopped and Docker restarted it" {
		t.Fatalf("restarted: %+v", evs)
	}
	if len(h.restarted) != 1 || h.svc.state(testInstance) != StateStarting {
		t.Errorf("not started again: %v %s", h.restarted, h.svc.state(testInstance))
	}
	if _, ok := h.svc.Usages()[testInstance]; ok {
		t.Error("an instance that isn't running is in Usages")
	}
	// It keeps stopping: no second alert while it's down.
	h.svc.setStatus(testInstance, StateRunning, nil)
	h.rt.set(containers.State{Running: false, ExitCode: 137, OOMKilled: true}, true)
	h.svc.checkAll(ctx)
	h.svc.markDown(ctx, testInstance, "it keeps stopping (exit code 1)")
	if evs := h.events(t); len(evs) != 0 {
		t.Fatalf("down again: %+v", evs)
	}

	// Wings restarts meanwhile; the instance answers again under the new
	// one: up, once.
	h.svc = h.open(t, t.TempDir())
	h.rt.set(containers.State{Running: true}, true)
	h.svc.markUp(ctx, testInstance)
	h.svc.markUp(ctx, testInstance)
	evs = h.events(t)
	if len(evs) != 1 || evs[0].Type != EventUp || evs[0].Data["instance"] != "main" {
		t.Fatalf("back up: %+v", evs)
	}

	// Its container gone: down. Stopped by someone then: forgotten, with
	// no "back up".
	h.svc.setStatus(testInstance, StateRunning, nil)
	h.rt.set(containers.State{}, false)
	h.svc.checkAll(ctx)
	if evs := h.events(t); len(evs) != 1 || evs[0].Data["error"] != "its container is gone" {
		t.Fatalf("gone: %+v", evs)
	}
	h.svc.forget(ctx, testInstance)
	h.svc.markUp(ctx, testInstance)
	if evs := h.events(t); len(evs) != 0 {
		t.Fatalf("after it was stopped: %+v", evs)
	}

	// An instance that shouldn't run isn't down.
	if err := h.db.Write.SetInstanceDesired(ctx, store.SetInstanceDesiredParams{DesiredState: StateStopped, ID: testInstance}); err != nil {
		t.Fatal(err)
	}
	h.svc.markDown(ctx, testInstance, "x")
	if evs := h.events(t); len(evs) != 0 {
		t.Fatalf("a stopped instance: %+v", evs)
	}
}

// A version change's end is an event, finished or failed.
func TestVersionEvent(t *testing.T) {
	ctx := context.Background()
	h := newHealthTest(t)
	in, err := h.svc.Get(ctx, testInstance)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.setChange(testInstance, &Change{Kind: "version", From: "11.4", To: "11.8"})
	h.svc.finishChange(ctx, in, nil)
	h.svc.setChange(testInstance, &Change{Kind: "patch", From: "11.8", To: "11.8"})
	h.svc.finishChange(ctx, in, errInitializing)
	evs := h.events(t)
	if len(evs) != 2 || evs[0].Type != EventVersion || evs[0].Data["ok"] != true || evs[0].Data["from"] != "11.4" ||
		evs[1].Data["ok"] != false || evs[1].Data["kind"] != "patch" || evs[1].Data["error"] == "" {
		t.Fatalf("%+v", evs)
	}
}

func TestInfoField(t *testing.T) {
	info := "# Clients\r\nconnected_clients:7\r\nblocked_clients:0\r\n"
	if n, err := infoField(info, "connected_clients"); err != nil || n != 7 {
		t.Errorf("%d %v", n, err)
	}
	if _, err := infoField(info, "nope"); err == nil {
		t.Error("a missing field read")
	}
}
