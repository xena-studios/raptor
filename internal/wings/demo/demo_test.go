package demo

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// fakeServers is the server manager as the demo sees it.
type fakeServers struct {
	mu       sync.Mutex
	servers  map[string]*server.Server
	deleted  []string
	failNext bool
	dir      string
	eventsCh chan server.Event
}

func (f *fakeServers) Get(_ context.Context, id string) (*server.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.servers[id]
	if !ok {
		return nil, server.ErrNotFound
	}
	return s, nil
}

func (f *fakeServers) List() map[string]server.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]server.State{}
	for id := range f.servers {
		out[id] = server.Running
	}
	return out
}

func (f *fakeServers) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errors.New("docker is busy")
	}
	delete(f.servers, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeServers) Events() (<-chan server.Event, func()) { return f.eventsCh, func() {} }

func (f *fakeServers) FilesDir(id string) (string, error) { return filepath.Join(f.dir, id), nil }

func newManager(t *testing.T, now *time.Time) (*Manager, *fakeServers, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg := Config{Key: pub, MaxSandboxes: 2}
	cfg.Defaults()
	srv := &fakeServers{servers: map[string]*server.Server{}, dir: t.TempDir(), eventsCh: make(chan server.Event)}
	m, err := New(context.Background(), Options{Config: cfg, DB: db, Servers: srv, Now: func() time.Time { return *now }, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	return m, srv, db
}

func (f *fakeServers) add(id, install string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers[id] = &server.Server{ID: id, InstallState: install}
}

// Wings keeps each sandbox's deadline itself and deletes it when it's up:
// InstallLimit to install, then Lifetime from when it's ready, once
// extended. It's in Wings' state, so a restart (or no Panel) changes
// nothing.
func TestSandboxDeadlines(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	m, srv, db := newManager(t, &now)

	// Two slots: a third create is refused, and a reservation counts.
	release, err := m.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	srv.add("a", "pending")
	if err := m.Add(ctx, Sandbox{ServerID: "a", EggID: "minecraft/paper"}); err != nil {
		t.Fatal(err)
	}
	release()
	srv.add("b", "pending")
	if err := m.Add(ctx, Sandbox{ServerID: "b", EggID: "standalone/terraria"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(); !errors.Is(err, ErrFull) || !errors.Is(m.Room(), ErrFull) {
		t.Fatalf("a third sandbox: %v", err)
	}

	// Not ready: no extending, and it has InstallLimit.
	if _, err := m.Extend(ctx, "a"); !errors.Is(err, ErrNotReady) {
		t.Errorf("extend while installing: %v", err)
	}
	if s, _ := m.Get("a"); s.Deadline != now.Add(limits.InstallLimit).Unix() {
		t.Errorf("install deadline: %d", s.Deadline)
	}

	// a's install finishes: 15 minutes from now.
	srv.add("a", "installed")
	now = now.Add(5 * time.Minute)
	m.Reap(ctx) // catches the install it wasn't told about
	a, _ := m.Get("a")
	if a.ReadyAt != now.Unix() || a.Deadline != now.Add(limits.Lifetime).Unix() {
		t.Fatalf("ready: %+v", a)
	}
	// Extended once.
	if a, err = m.Extend(ctx, "a"); err != nil || a.Deadline != now.Add(limits.Lifetime+limits.Extension).Unix() {
		t.Fatalf("extend: %+v %v", a, err)
	}
	if _, err := m.Extend(ctx, "a"); !errors.Is(err, ErrExtended) {
		t.Errorf("a second extension: %v", err)
	}
	if _, err := m.Extend(ctx, "nope"); !errors.Is(err, ErrNotBox) {
		t.Errorf("extending a server that isn't a sandbox: %v", err)
	}

	// A restart loses nothing: the deadlines are in the state database.
	m2, err := New(ctx, Options{Config: m.Config(), DB: db, Servers: srv, Now: func() time.Time { return now }, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m2.Get("a"); !ok || got.Deadline != a.Deadline || !got.Extended {
		t.Fatalf("after a restart: %+v", got)
	}

	// b never finishes installing: deleted InstallLimit after it was made
	// (5 minutes ago). a lasts its 30.
	now = now.Add(limits.InstallLimit - 5*time.Minute)
	m2.Reap(ctx)
	if len(srv.deleted) != 1 || srv.deleted[0] != "b" || m2.IsSandbox("b") {
		t.Fatalf("after the install limit: deleted %v", srv.deleted)
	}
	// A failed delete is tried again on the next pass.
	now = time.Unix(a.Deadline, 0)
	srv.failNext = true
	m2.Reap(ctx)
	if !m2.IsSandbox("a") {
		t.Fatal("forgot a sandbox it couldn't delete")
	}
	m2.Reap(ctx)
	if m2.IsSandbox("a") || len(srv.deleted) != 2 {
		t.Fatalf("a after its time: deleted %v", srv.deleted)
	}
	// A server deleted some other way is forgotten, not deleted again.
	srv.add("c", "installed")
	if err := m2.Add(ctx, Sandbox{ServerID: "c"}); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	delete(srv.servers, "c")
	srv.mu.Unlock()
	m2.Reap(ctx)
	if m2.IsSandbox("c") || len(srv.deleted) != 2 {
		t.Errorf("a server deleted elsewhere: %v", srv.deleted)
	}
}

// A template build is captured into the templates when its install
// finishes, then deleted; a new sandbox of the same egg can start from it.
func TestWarmTemplates(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	m, srv, _ := newManager(t, &now)
	tpl := &Templates{Dir: filepath.Join(t.TempDir(), "demo-templates"), UID: os.Getuid(), GID: os.Getgid(), Now: func() time.Time { return now }}
	m.o.Templates = tpl
	key := TemplateKey("fp", "img", map[string]string{"V": "1"}, true)
	if key == TemplateKey("fp", "img", map[string]string{"V": "2"}, true) || key == TemplateKey("fp", "img", map[string]string{"V": "1"}, false) {
		t.Fatal("a template key ignores the variables or the EULA")
	}
	dir := filepath.Join(srv.dir, "w")
	if err := os.MkdirAll(filepath.Join(dir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "world", "level.dat"), []byte("level"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.add("w", "installed")
	if err := m.Add(ctx, Sandbox{ServerID: "w", EggID: "minecraft/paper", Template: key, Warm: true}); err != nil {
		t.Fatal(err)
	}
	m.installed(ctx, "w")
	if !tpl.Has(key) || m.IsSandbox("w") || len(srv.deleted) != 1 {
		t.Fatalf("after the build: has %v, sandbox %v, deleted %v", tpl.Has(key), m.IsSandbox("w"), srv.deleted)
	}
	if got := tpl.List(); got[key] != "minecraft/paper" {
		t.Errorf("templates: %v", got)
	}
	out := t.TempDir()
	if err := tpl.CopyTo(ctx, key, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "world", "level.dat")); string(b) != "level" {
		t.Errorf("copied: %q", b)
	}
	// Stale ones go, and beyond the number kept.
	now = now.Add(MaxTemplateAge)
	if tpl.Has(key) {
		t.Error("a stale template is used")
	}
	tpl.Prune(6)
	if _, err := os.Stat(filepath.Join(tpl.Dir, key)); !os.IsNotExist(err) {
		t.Errorf("a stale template is kept: %v", err)
	}
	if tpl.Has("../../etc") || tpl.CopyTo(ctx, "../../etc", out) == nil {
		t.Error("a key that isn't one")
	}
}

func TestConfig(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	ok := Config{Key: pub}
	ok.Defaults()
	if err := ok.Validate(); err != nil || ok.MaxSandboxes != limits.DefaultMaxSandboxes || ok.PortMin != limits.DefaultPortMin {
		t.Fatalf("defaults: %+v %v", ok, err)
	}
	for name, c := range map[string]Config{
		"no key":        {MaxSandboxes: 4, PortMin: 30000, PortMax: 30099},
		"too many":      {Key: pub, MaxSandboxes: 99, PortMin: 30000, PortMax: 30099},
		"low ports":     {Key: pub, MaxSandboxes: 4, PortMin: 80, PortMax: 90},
		"too few ports": {Key: pub, MaxSandboxes: 4, PortMin: 30000, PortMax: 30001},
	} {
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Off by default, on once saved, off again.
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if c, err := Load(ctx, db); c != nil || err != nil {
		t.Fatalf("a new node: %v %v", c, err)
	}
	if err := Save(ctx, db, &Config{Key: pub}); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(ctx, db); c == nil || err != nil || string(c.Key) != string(pub) {
		t.Fatalf("after demo on: %v %v", c, err)
	}
	if err := Save(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(ctx, db); c != nil || err != nil {
		t.Fatalf("after demo off: %v %v", c, err)
	}
}
