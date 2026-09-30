package files

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const srvID = "0199a0b1-7c2e-7a41-9f3e-2b1c4d5e6f70"

var errInstalling = errors.New("server is installing")

// servers is a fake server manager with one server.
type servers struct {
	dir string

	mu    sync.Mutex
	err   error // CheckFiles
	deny  []string
	space int64
}

func (s *servers) FilesDir(id string) (string, error) {
	if id != srvID {
		return "", errors.New("server not found")
	}
	return s.dir, nil
}

func (s *servers) CheckFiles(context.Context, string, bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *servers) Denylist(string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deny, nil
}

func (s *servers) SpaceLeft(context.Context, string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.space, nil
}

type env struct {
	t       *testing.T
	dir     string
	outside string // a sibling of the server's directory
	db      *store.DB
	servers *servers
	jobs    *jobs.Engine
	svc     *Service
	now     time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, dir: filepath.Join(root, "server"), outside: filepath.Join(root, "outside"), now: time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC)}
	for _, d := range []string{e.dir, e.outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(context.Background(), filepath.Join(root, "state.db"))
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
	e.db = db
	e.servers = &servers{dir: e.dir, space: -1}
	e.jobs = jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(root, "jobs"), Poll: 10 * time.Millisecond})
	e.svc = NewService(Options{
		Servers: e.servers, Store: db, Jobs: e.jobs, Events: events.New(db),
		UID: os.Getuid(), GID: os.Getgid(), Now: func() time.Time { return e.now },
	})
	if err := e.jobs.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.jobs.Close)
	return e
}

func (e *env) write(name, data string) {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) read(name string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(e.dir, name))
	return err == nil
}

func (e *env) wait(id string) jobs.Job {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	j, err := e.jobs.Wait(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

var ctx = context.Background()

func TestOperations(t *testing.T) {
	e := newEnv(t)
	e.write("server.properties", "motd=hi")
	e.write("world/level.dat", "level")

	l, err := e.svc.List(ctx, srvID, "/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range l.Entries {
		names = append(names, en.Name+":"+en.Type)
	}
	if !slices.Equal(names, []string{"server.properties:file", "world:dir"}) {
		t.Fatalf("listing = %v", names)
	}

	c, err := e.svc.Read(ctx, srvID, "/server.properties")
	if err != nil || string(c.Data) != "motd=hi" || c.Size != 7 {
		t.Fatalf("read = %+v, %v", c, err)
	}
	if err := e.svc.Write(ctx, srvID, "config/new.yml", []byte("a: 1")); err != nil {
		t.Fatal(err)
	}
	if got := e.read("config/new.yml"); got != "a: 1" {
		t.Fatalf("written = %q", got)
	}
	fi, _ := os.Stat(filepath.Join(e.dir, "config/new.yml"))
	if fi.Mode().Perm() != FileMode {
		t.Errorf("new file mode = %v", fi.Mode())
	}
	if err := e.svc.Write(ctx, srvID, "big", make([]byte, MaxEdit+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("writing over the editor limit: %v", err)
	}

	if err := e.svc.Mkdir(ctx, srvID, "a/b/c"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Mkdir(ctx, srvID, "a/b/c"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("mkdir of an existing directory: %v", err)
	}

	cp, err := e.svc.Copy(ctx, srvID, "server.properties")
	if err != nil || cp != "server copy.properties" || e.read(cp) != "motd=hi" {
		t.Fatalf("copy = %q, %v", cp, err)
	}
	if cp, _ = e.svc.Copy(ctx, srvID, "server.properties"); cp != "server copy 2.properties" {
		t.Errorf("second copy = %q", cp)
	}

	// Rename, relative to a directory; never over an existing file.
	if err := e.svc.Rename(ctx, srvID, "world", []Move{{From: "level.dat", To: "../level.bak"}}); err != nil {
		t.Fatal(err)
	}
	if !e.exists("level.bak") || e.exists("world/level.dat") {
		t.Error("rename didn't move the file")
	}
	if err := e.svc.Rename(ctx, srvID, "/", []Move{{From: "level.bak", To: "server.properties"}}); !errors.Is(err, fs.ErrExist) {
		t.Errorf("rename over an existing file: %v", err)
	}
	if err := e.svc.Rename(ctx, srvID, "/", []Move{{From: "/", To: "x"}}); !errors.Is(err, ErrRoot) {
		t.Errorf("renaming the directory itself: %v", err)
	}

	if err := e.svc.Chmod(ctx, srvID, "/", []Chmod{{Name: "level.bak", Mode: 0o600}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Chmod(ctx, srvID, "/", []Chmod{{Name: "level.bak", Mode: 0o4755}}); err == nil {
		t.Error("setuid was accepted")
	}
	if fi, _ := os.Stat(filepath.Join(e.dir, "level.bak")); fi.Mode() != 0o600 {
		t.Errorf("mode = %v", fi.Mode())
	}

	if err := e.svc.Delete(ctx, srvID, "/", []string{"a", "level.bak", "missing"}); err != nil {
		t.Fatal(err)
	}
	if e.exists("a") || e.exists("level.bak") {
		t.Error("delete left files")
	}
	if err := e.svc.Delete(ctx, srvID, "/", []string{"/"}); !errors.Is(err, ErrRoot) {
		t.Errorf("deleting the directory itself: %v", err)
	}
	if !e.exists("server.properties") {
		t.Error("deleting the directory removed files")
	}
}

// Nothing may be touched while the server installs or restores.
func TestUnavailable(t *testing.T) {
	e := newEnv(t)
	e.write("a", "a")
	e.servers.err = errInstalling
	checks := map[string]error{}
	_, checks["list"] = e.svc.List(ctx, srvID, "/")
	_, checks["read"] = e.svc.Read(ctx, srvID, "a")
	checks["write"] = e.svc.Write(ctx, srvID, "a", nil)
	checks["delete"] = e.svc.Delete(ctx, srvID, "/", []string{"a"})
	_, checks["upload"] = e.svc.StartUpload(ctx, srvID, "u", "b", 1)
	_, checks["download"] = e.svc.StartDownload(ctx, srvID, "a")
	_, checks["compress"] = e.svc.Compress(ctx, srvID, "u", "/", []string{"a"})
	for op, err := range checks {
		if !errors.Is(err, errInstalling) {
			t.Errorf("%s while installing: %v", op, err)
		}
	}
}

// Files planted by the server or its install script: links out of the
// directory, FIFOs, device-like files. None may be followed out, block, or
// be opened.
func TestHostileFiles(t *testing.T) {
	e := newEnv(t)
	secret := filepath.Join(e.outside, "secret")
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"abs-link": secret, "rel-link": "../outside/secret", "dir-link": "../outside"} {
		if err := os.Symlink(target, filepath.Join(e.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(e.dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, name := range []string{"abs-link", "rel-link", "dir-link/secret", "fifo"} {
			if _, err := e.svc.Read(ctx, srvID, name); err == nil {
				t.Errorf("read %s", name)
			}
			if err := e.svc.Write(ctx, srvID, name, []byte("x")); err == nil {
				t.Errorf("wrote %s", name)
			}
			if _, err := e.svc.StartDownload(ctx, srvID, name); err == nil {
				t.Errorf("downloaded %s", name)
			}
			if _, err := e.svc.Copy(ctx, srvID, name); err == nil {
				t.Errorf("copied %s", name)
			}
		}
		if _, err := e.svc.List(ctx, srvID, "dir-link"); err == nil {
			t.Error("listed a directory outside")
		}
		if err := e.svc.Rename(ctx, srvID, "/", []Move{{From: "fifo", To: "dir-link/fifo"}}); err == nil {
			t.Error("moved a file out through a link")
		}
		if err := e.svc.Mkdir(ctx, srvID, "dir-link/new"); err == nil {
			t.Error("created a directory outside")
		}
		// Links are listed with their target, and deleting removes the link.
		l, err := e.svc.List(ctx, srvID, "/")
		if err != nil {
			t.Error(err)
			return
		}
		for _, en := range l.Entries {
			if en.Name == "rel-link" && (en.Type != "symlink" || en.Target != "../outside/secret" || en.TargetType != "") {
				t.Errorf("link entry = %+v", en)
			}
		}
		if err := e.svc.Delete(ctx, srvID, "/", []string{"abs-link", "dir-link", "fifo"}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("an operation blocked")
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "host secret" {
		t.Errorf("the file outside changed: %q, %v", b, err)
	}
	if entries, _ := os.ReadDir(e.outside); len(entries) != 1 {
		t.Errorf("files were created outside: %v", entries)
	}
}

func TestDenied(t *testing.T) {
	e := newEnv(t)
	e.servers.deny = []string{"server.jar", "secrets/", "!secrets/ok"}
	e.write("server.jar", "jar")
	e.write("secrets/token", "t")
	e.write("data/a", "a")
	e.write("x/y/server.jar", "jar")
	if err := os.Symlink("../secrets/token", filepath.Join(e.dir, "data/link")); err != nil {
		t.Fatal(err)
	}

	l, err := e.svc.List(ctx, srvID, "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range l.Entries {
		want := en.Name == "server.jar" || en.Name == "secrets"
		if en.Denied != want {
			t.Errorf("%s: denied = %v", en.Name, en.Denied)
		}
	}

	checks := map[string]error{}
	_, checks["read"] = e.svc.Read(ctx, srvID, "server.jar")
	_, checks["read through a link"] = e.svc.Read(ctx, srvID, "data/link")
	checks["write"] = e.svc.Write(ctx, srvID, "server.jar", nil)
	checks["create inside"] = e.svc.Write(ctx, srvID, "secrets/ok", nil)
	checks["delete"] = e.svc.Delete(ctx, srvID, "/", []string{"server.jar"})
	checks["delete the parent"] = e.svc.Delete(ctx, srvID, "/", []string{"x"})
	checks["move the parent"] = e.svc.Rename(ctx, srvID, "/", []Move{{From: "x", To: "z"}})
	checks["move onto"] = e.svc.Rename(ctx, srvID, "/", []Move{{From: "data/a", To: "server.jar"}})
	checks["move out"] = e.svc.Rename(ctx, srvID, "/", []Move{{From: "secrets", To: "open"}})
	checks["chmod"] = e.svc.Chmod(ctx, srvID, "/", []Chmod{{Name: "server.jar", Mode: 0o777}})
	_, checks["copy"] = e.svc.Copy(ctx, srvID, "server.jar")
	_, checks["download"] = e.svc.StartDownload(ctx, srvID, "secrets/token")
	_, checks["upload"] = e.svc.StartUpload(ctx, srvID, "u", "server.jar", 1)
	_, checks["decompress"] = e.svc.Decompress(ctx, srvID, "u", "server.jar", "")
	for op, err := range checks {
		if !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v", op, err)
		}
	}
	// A directory with a denied file moves if the names stay allowed.
	e.servers.deny = []string{"/y/server.jar"}
	if err := e.svc.Rename(ctx, srvID, "/", []Move{{From: "x/y", To: "y"}}); !errors.Is(err, ErrDenied) {
		t.Errorf("moving a file onto a denied name: %v", err)
	}
	if err := e.svc.Rename(ctx, srvID, "/", []Move{{From: "x", To: "w"}}); err != nil {
		t.Error(err)
	}
	if e.read("server.jar") != "jar" || e.read("secrets/token") != "t" {
		t.Error("a denied file changed")
	}
}

func TestReadLimit(t *testing.T) {
	e := newEnv(t)
	e.write("big.log", string(bytes.Repeat([]byte("x"), MaxEdit+1)))
	if _, err := e.svc.Read(ctx, srvID, "big.log"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("read over the editor limit: %v", err)
	}
}
