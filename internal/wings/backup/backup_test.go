package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const (
	srvID   = "0199a000-0000-7000-8000-000000000001"
	otherID = "0199a000-0000-7000-8000-000000000002" // another server on the node
)

// servers is a fake server manager around a real directory.
type servers struct {
	mu       sync.Mutex
	state    server.State
	console  *server.Console
	src      server.BackupSource
	confirm  bool // answer "save-all" with the egg's wait_for line
	calls    []string
	restores []bool // the start flag of each restore
	deleted  bool   // srvID was deleted
	other    bool   // otherID exists
}

func (f *servers) Status(id string) (server.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if (id != srvID || f.deleted) && (id != otherID || !f.other) {
		return server.Status{}, server.ErrNotFound
	}
	return server.Status{State: f.state, Console: f.console}, nil
}

func (f *servers) SendCommand(_, user, cmd string) error {
	f.mu.Lock()
	f.calls = append(f.calls, user+": "+cmd)
	confirm := f.confirm && cmd == "save-all"
	f.mu.Unlock()
	if confirm {
		f.console.Write("[Server thread/INFO]: Saved the game")
	}
	return nil
}

func (f *servers) BackupSource(context.Context, string) (server.BackupSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.src, nil
}

func (f *servers) Restore(ctx context.Context, _ string, start bool, fn func(context.Context, string) error) error {
	f.mu.Lock()
	f.restores = append(f.restores, start)
	prev := f.state
	f.state = server.Restoring
	f.mu.Unlock()
	err := fn(ctx, f.src.Dir)
	f.mu.Lock()
	f.state = prev
	f.mu.Unlock()
	return err
}

func (f *servers) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t       *testing.T
	db      *store.DB
	dir     string
	servers *servers
	events  *events.Outbox
	jobs    *jobs.Engine
	clock   *clock
	m       *Manager
	free    int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Write.InsertServer(context.Background(), store.InsertServerParams{
		ID: srvID, Name: "smp", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "volumes", srvID)
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	v := &env{
		t: t, db: db, dir: dir, events: events.New(db), free: 1 << 40,
		clock: &clock{t: time.Date(2026, 3, 2, 4, 0, 0, 0, time.UTC)},
		servers: &servers{
			state: server.Offline, console: server.NewConsole(),
			src: server.BackupSource{Dir: data, UID: -1, GID: -1},
		},
	}
	v.jobs = jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(dir, "jobs"), Poll: 20 * time.Millisecond})
	v.m = New(Options{
		Store: db, Jobs: v.jobs, Events: v.events, Servers: v.servers,
		LocalPath: filepath.Join(dir, "backups"), StateDir: filepath.Join(dir, "kopia"),
		FreeSpace: func(string) (int64, error) { return v.free, nil }, MinFree: 10 << 30,
		HookTimeout: 300 * time.Millisecond, Now: v.clock.now,
	})
	if err := v.jobs.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.jobs.Close)
	return v
}

func (v *env) write(name, data string) {
	v.t.Helper()
	p := filepath.Join(v.servers.src.Dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		v.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		v.t.Fatal(err)
	}
}

func (v *env) read(name string) string {
	b, err := os.ReadFile(filepath.Join(v.servers.src.Dir, name))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func (v *env) wait(jobID string) jobs.Job {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, err := v.jobs.Wait(ctx, jobID)
	if err != nil {
		v.t.Fatal(err)
	}
	return j
}

// backup makes a manual backup and waits for it.
func (v *env) backup() *Backup {
	v.t.Helper()
	b, err := v.m.Create(context.Background(), srvID, CreateOptions{Kind: KindManual, User: "u1"})
	if err != nil {
		v.t.Fatal(err)
	}
	if j := v.wait(b.JobID); j.Status != jobs.Succeeded {
		log, _ := v.jobs.Log(j.ID)
		v.t.Fatalf("backup job %s: %s\n%s", j.Status, j.Error, log)
	}
	got, err := v.m.Get(context.Background(), srvID, b.ID)
	if err != nil {
		v.t.Fatal(err)
	}
	return got
}

func (v *env) eventTypes() []string {
	all, err := v.events.Since(context.Background(), 0, 1000)
	if err != nil {
		v.t.Fatal(err)
	}
	var out []string
	for _, e := range all {
		if strings.HasPrefix(e.Type, "backup.") {
			out = append(out, e.Type)
		}
	}
	return out
}

func (v *env) list() []*Backup {
	v.t.Helper()
	l, err := v.m.List(context.Background(), srvID)
	if err != nil {
		v.t.Fatal(err)
	}
	return l
}

func TestBackupAndRestore(t *testing.T) {
	v := newEnv(t)
	v.write("world/level.dat", "day 1")
	b := v.backup()
	if b.Status != StatusOK || b.Files != 1 || b.Size != 5 || b.Kind != KindManual || b.CreatedBy != "u1" {
		t.Fatalf("backup = %+v", b)
	}

	v.write("world/level.dat", "day 2, griefed")
	v.write("griefer.txt", "x")
	v.servers.src.DesiredRunning = true
	jobID, err := v.m.Restore(context.Background(), srvID, b.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(jobID); j.Status != jobs.Succeeded {
		t.Fatalf("restore: %s %s", j.Status, j.Error)
	}
	if got := v.read("world/level.dat"); got != "day 1" {
		t.Fatalf("level.dat = %q after restore", got)
	}
	if _, err := os.Stat(filepath.Join(v.servers.src.Dir, "griefer.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file newer than the backup survived the restore")
	}
	if !slices.Equal(v.servers.restores, []bool{true}) {
		t.Fatalf("restores = %v, want one that starts the server after", v.servers.restores)
	}

	// The files that were replaced are in a safety backup, which expires.
	list := v.list()
	if len(list) != 2 || list[0].Kind != KindSafety || list[0].Status != StatusOK || list[0].ExpiresAt.Sub(list[0].CreatedAt) != SafetyTTL {
		t.Fatalf("backups after restore: %+v", list)
	}
	jobID, err = v.m.Restore(context.Background(), srvID, list[0].ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	v.wait(jobID)
	if got := v.read("world/level.dat"); got != "day 2, griefed" {
		t.Fatalf("level.dat = %q after restoring the safety backup", got)
	}

	want := []string{EventQueued, EventFinished, EventRestoreQueued, EventQueued, EventFinished, EventRestored}
	if got := v.eventTypes(); !slices.Equal(got[:len(want)], want) {
		t.Fatalf("events = %v", got)
	}

	// Retention never counts or deletes safety backups; they expire.
	if err := v.m.SetPolicy(context.Background(), srvID, Policy{Retention: Retention{KeepLast: 1}}); err != nil {
		t.Fatal(err)
	}
	v.clock.add(time.Hour)
	latest := v.backup()
	var kinds []string
	for _, b := range v.list() {
		kinds = append(kinds, b.Kind)
		if b.Kind == KindManual && b.ID != latest.ID {
			t.Errorf("retention kept %s", b.ID)
		}
	}
	if !slices.Equal(kinds, []string{KindManual, KindSafety, KindSafety}) {
		t.Fatalf("after retention: %v", kinds)
	}
	v.clock.add(SafetyTTL)
	v.m.housekeeping(context.Background())
	deadline := time.Now().Add(10 * time.Second)
	for len(v.list()) > 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if l := v.list(); len(l) != 1 || l[0].ID != latest.ID {
		t.Fatalf("expired safety backups remain: %+v", l)
	}
}

// A restore bigger than the server's disk limit changes nothing and leaves
// the server stopped.
func TestRestoreTooLarge(t *testing.T) {
	v := newEnv(t)
	v.write("big", strings.Repeat("x", 4096))
	b := v.backup()
	v.write("big", "small now")
	v.servers.src.DiskLimit = 1024
	jobID, err := v.m.Restore(context.Background(), srvID, b.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	j := v.wait(jobID)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "larger than") {
		t.Fatalf("restore: %s %q", j.Status, j.Error)
	}
	if got := v.read("big"); got != "small now" {
		t.Fatalf("a refused restore changed the files: %q", got)
	}
}

func TestHooks(t *testing.T) {
	v := newEnv(t)
	v.write("f", "x")
	v.servers.state = server.Running
	v.servers.src.Pre = []string{"save-off", "save-all"}
	v.servers.src.Post = []string{"save-on"}
	v.servers.src.WaitFor = "Saved the game"
	v.servers.confirm = true
	b := v.backup()
	want := []string{"backup: save-off", "backup: save-all", "backup: save-on"}
	if got := v.servers.got(); !slices.Equal(got, want) || b.Warning != "" {
		t.Fatalf("commands %q, warning %q", got, b.Warning)
	}

	// No confirmation: the backup is taken anyway, with a warning, and the
	// post commands still run.
	v.servers.confirm = false
	b = v.backup()
	if !strings.Contains(b.Warning, "didn't confirm the save") || !slices.Equal(v.servers.got()[3:], want) {
		t.Fatalf("unconfirmed: warning %q, commands %q", b.Warning, v.servers.got())
	}

	// A failed backup still sends the post commands.
	good := v.servers.src.Dir
	v.servers.src.Dir = filepath.Join(v.dir, "missing")
	b, err := v.m.Create(context.Background(), srvID, CreateOptions{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(b.JobID); j.Status != jobs.Failed {
		t.Fatalf("backup of a missing directory: %s", j.Status)
	}
	if got := v.servers.got(); got[len(got)-1] != "backup: save-on" {
		t.Fatalf("after a failed backup: %q", got)
	}
	if b, _ := v.m.Get(context.Background(), srvID, b.ID); b.Status != StatusFailed || b.Error == "" {
		t.Fatalf("failed backup row: %+v", b)
	}
	v.servers.src.Dir = good

	// An offline server gets no hooks.
	v.servers.state = server.Offline
	v.backup()
	if n := len(v.servers.got()); n != 9 {
		t.Fatalf("hooks ran for an offline server: %q", v.servers.got())
	}
}

func TestRetention(t *testing.T) {
	v := newEnv(t)
	v.write("f", "x")
	if err := v.m.SetPolicy(context.Background(), srvID, Policy{Retention: Retention{KeepLast: 2}}); err != nil {
		t.Fatal(err)
	}
	locked := v.backup()
	if err := v.m.Lock(context.Background(), srvID, locked.ID, true); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 4 {
		v.clock.add(time.Hour)
		v.write("f", strings.Repeat("y", i+1))
		ids = append(ids, v.backup().ID)
	}
	var got []string
	for _, b := range v.list() {
		got = append(got, b.ID)
	}
	want := []string{ids[3], ids[2], locked.ID}
	if !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	// The deleted backups' snapshots are gone too.
	e := &engine.Engine{Dest: engine.Destination{ID: "local", Type: engine.Local, Path: v.m.o.LocalPath}, StateDir: t.TempDir()}
	e.Password, _ = v.m.password(context.Background())
	if _, err := e.Restore(context.Background(), engine.RestoreRequest{SnapshotID: "x", Dir: t.TempDir(), UID: -1, GID: -1}, nil); err == nil {
		t.Fatal("restored a snapshot that doesn't exist")
	}
}

func TestDeleteAndServerDeleted(t *testing.T) {
	v := newEnv(t)
	v.write("f", "x")
	a, b := v.backup(), v.backup()
	jobID, err := v.m.Delete(context.Background(), srvID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	v.wait(jobID)
	if l := v.list(); len(l) != 1 || l[0].ID != b.ID {
		t.Fatalf("after delete: %+v", l)
	}
	if _, err := v.m.Delete(context.Background(), "0199a000-0000-7000-8000-000000000002", b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting another server's backup: %v", err)
	}

	v.m.ServerDeleted(context.Background(), srvID)
	deadline := time.Now().Add(10 * time.Second)
	for len(v.list()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if l := v.list(); len(l) != 0 {
		t.Fatalf("local backups of a deleted server remain: %+v", l)
	}
}

func TestLowDisk(t *testing.T) {
	v := newEnv(t)
	v.free = 1 << 30
	if _, err := v.m.Create(context.Background(), srvID, CreateOptions{Kind: KindManual}); !errors.Is(err, ErrLowDisk) {
		t.Fatalf("err = %v, want ErrLowDisk", err)
	}
}

func TestPolicyAndDestinations(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	if p, err := v.m.Policy(ctx, srvID); err != nil || p.DestinationID != LocalDestination || p.Retention != DefaultRetention {
		t.Fatalf("default policy = %+v, %v", p, err)
	}
	for name, p := range map[string]Policy{
		"keeps nothing": {},
		"negative":      {Retention: Retention{KeepLast: -1}},
		"huge":          {Retention: Retention{KeepDaily: 5000}},
		"bad ignore":    {Retention: DefaultRetention, Ignore: []string{"a\nb"}},
	} {
		if err := v.m.SetPolicy(ctx, srvID, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := v.m.SetPolicy(ctx, srvID, Policy{DestinationID: "nope", Retention: DefaultRetention}); !errors.Is(err, ErrDestination) {
		t.Errorf("unknown destination: %v", err)
	}

	s3 := Destination{Name: "B2", Type: engine.S3, S3: engine.S3Config{Endpoint: "s3.example.com", Bucket: "b", AccessKey: "k", SecretKey: "secret"}}
	for name, mutate := range map[string]func(*Destination){
		"no name":   func(d *Destination) { d.Name = "" },
		"local":     func(d *Destination) { d.Type = engine.Local },
		"no bucket": func(d *Destination) { d.S3.Bucket = "" },
		"no secret": func(d *Destination) { d.S3.SecretKey = "" },
		"ftp":       func(d *Destination) { d.S3.Endpoint = "ftp://x" },
	} {
		d := s3
		mutate(&d)
		if _, err := v.m.SaveDestination(ctx, d); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	id, err := v.m.SaveDestination(ctx, s3)
	if err != nil {
		t.Fatal(err)
	}
	// An update without the secret keeps it.
	upd := s3
	upd.ID, upd.Name, upd.S3.SecretKey = id, "B2 renamed", ""
	if _, err := v.m.SaveDestination(ctx, upd); err != nil {
		t.Fatal(err)
	}
	ed, err := v.m.engineDest(ctx, id)
	if err != nil || ed.S3.SecretKey != "secret" {
		t.Fatalf("stored destination = %+v, %v", ed, err)
	}
	list, err := v.m.Destinations(ctx)
	if err != nil || len(list) != 2 || list[1].Name != "B2 renamed" || list[1].S3.SecretKey == "secret" {
		t.Fatalf("destinations = %+v, %v", list, err)
	}
	all, _ := v.events.Since(ctx, 0, 1000)
	for _, e := range all {
		if b, _ := json.Marshal(e.Data); strings.Contains(e.Type, "destination") && strings.Contains(string(b), `"secret"`) {
			t.Fatalf("an event has the secret key: %s", b)
		}
	}

	if err := v.m.SetPolicy(ctx, srvID, Policy{DestinationID: id, Retention: DefaultRetention}); err != nil {
		t.Fatal(err)
	}
	if err := v.m.DeleteDestination(ctx, id); !errors.Is(err, ErrInUse) {
		t.Fatalf("deleting a destination in use: %v", err)
	}
	if err := v.m.DeleteDestination(ctx, LocalDestination); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deleting the local destination: %v", err)
	}
	if _, err := v.m.SaveDestination(ctx, Destination{ID: LocalDestination, Name: "x", Type: engine.S3}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changing the local destination: %v", err)
	}
	if err := v.m.SetPolicy(ctx, srvID, DefaultPolicy()); err != nil {
		t.Fatal(err)
	}
	if err := v.m.DeleteDestination(ctx, id); err != nil {
		t.Fatal(err)
	}
}

// Backups whose job is gone (their server was deleted mid-backup) are
// marked failed on the next start.
func TestCleanupInterrupted(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	b := &Backup{ID: "0199a000-0000-7000-8000-0000000000b1", ServerID: srvID, DestinationID: LocalDestination, Kind: KindManual, JobID: "gone", CreatedAt: v.clock.now()}
	if err := v.db.WriteTx(ctx, func(q *store.Queries) error { return v.m.insert(ctx, q, b) }); err != nil {
		t.Fatal(err)
	}
	if err := v.m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.m.Close)
	got, err := v.m.Get(ctx, srvID, b.ID)
	if err != nil || got.Status != StatusFailed || got.Error != "interrupted" {
		t.Fatalf("interrupted backup = %+v, %v", got, err)
	}
}

// Backups a job takes for itself: a wipe's safety backup and a deletion's
// final backup. A resumed job gets the same backup back.
func TestJobBackups(t *testing.T) {
	ctx := context.Background()
	v := newEnv(t)
	if id, err := v.m.JobBackup(ctx, srvID, "job-empty", KindFinal, io.Discard); err != nil || id != "" {
		t.Fatalf("empty directory: %q, %v", id, err)
	}
	v.write("world/level.dat", "level")
	if _, err := v.m.JobBackup(ctx, srvID, "job-x", KindManual, io.Discard); !errors.Is(err, ErrInvalid) {
		t.Errorf("manual kind: %v", err)
	}
	final, err := v.m.JobBackup(ctx, srvID, "job-delete", KindFinal, io.Discard)
	if err != nil || final == "" {
		t.Fatalf("final backup: %q, %v", final, err)
	}
	again, err := v.m.JobBackup(ctx, srvID, "job-delete", KindFinal, io.Discard)
	if err != nil || again != final {
		t.Errorf("resumed job: %q, %v; want %q", again, err, final)
	}
	safety, err := v.m.JobBackup(ctx, srvID, "job-wipe", KindSafety, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	now := v.clock.now()
	for id, want := range map[string]time.Time{final: now.Add(FinalTTL), safety: now.Add(SafetyTTL)} {
		b, err := v.m.Get(ctx, srvID, id)
		if err != nil || b.Status != StatusOK || !b.ExpiresAt.Equal(want) {
			t.Errorf("backup %s: %+v, %v; want expiry %v", id, b, err, want)
		}
	}
	if n := len(v.list()); n != 2 {
		t.Errorf("%d backups, want 2", n)
	}

	// Deleting the server deletes its other local backups, not the final one.
	manual := v.backup()
	v.m.ServerDeleted(ctx, srvID)
	deadline := time.Now().Add(10 * time.Second)
	for len(v.list()) > 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if l := v.list(); len(l) != 1 || l[0].ID != final {
		t.Fatalf("after the server was deleted: %+v (manual %s)", l, manual.ID)
	}
}

// A deleted server's backup restores onto another server; an existing
// server's doesn't.
func TestRestoreFromDeletedServer(t *testing.T) {
	ctx := context.Background()
	v := newEnv(t)
	v.write("world/level.dat", "level")
	final, err := v.m.JobBackup(ctx, srvID, "job-delete", KindFinal, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	v.servers.other = true
	if _, err := v.m.Restore(ctx, otherID, final, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restoring an existing server's backup onto another: %v", err)
	}
	if from, err := v.m.FromDeletedServer(ctx, srvID, final); err != nil || from {
		t.Errorf("own backup: %v, %v", from, err)
	}

	v.servers.mu.Lock()
	v.servers.deleted = true
	v.servers.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(v.servers.src.Dir, "world")); err != nil {
		t.Fatal(err)
	}
	if from, err := v.m.FromDeletedServer(ctx, otherID, final); err != nil || !from {
		t.Errorf("deleted server's backup: %v, %v", from, err)
	}
	job, err := v.m.Restore(ctx, otherID, final, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(job); j.Status != jobs.Succeeded {
		t.Fatalf("restore: %s %s", j.Status, j.Error)
	}
	if got := v.read("world/level.dat"); got != "level" {
		t.Errorf("restored file = %q", got)
	}
}
