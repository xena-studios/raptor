package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const srvID = "0b6f7a3e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"

var allPerms = []string{PermSFTP, PermRead, PermWrite}

// panel is a fake Authenticator: alice's password is "hunter2". down makes
// it unreachable.
type panel struct {
	mu      sync.Mutex
	down    bool
	perms   []string
	expires time.Time // the password's expiry
}

func (p *panel) Password(_ context.Context, l Login, pw string) (Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return Grant{}, ErrUnavailable
	}
	if l.Username != "alice" || pw != "hunter2" {
		return Grant{}, ErrDenied
	}
	return Grant{UserID: "u1", Permissions: p.perms, ExpiresAt: p.expires}, nil
}

func (p *panel) set(fn func(p *panel)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// servers is a fake Servers with one server.
type servers struct {
	dir      string
	mu       sync.Mutex
	err      error // returned by CheckFiles
	errWrite error // returned by CheckFiles for writes (over the disk limit)
	deny     []string
}

func (s *servers) Denylist(string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deny, nil
}

func (s *servers) Resolve(ref string) (string, error) {
	if ref == srvID || ref == srvID[len(srvID)-8:] {
		return srvID, nil
	}
	return "", errors.New("server not found")
}

func (s *servers) FilesDir(id string) (string, error) {
	if id != srvID {
		return "", errors.New("server not found")
	}
	return s.dir, nil
}

func (s *servers) CheckFiles(_ context.Context, _ string, write bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil && write {
		return s.errWrite
	}
	return s.err
}

type env struct {
	t       *testing.T
	root    string // parent of the server's directory
	dir     string // the server's directory
	db      *store.DB
	panel   *panel
	servers *servers
	srv     *Server
	addr    string
	hostKey ssh.Signer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, srvID)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
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
	hk, err := LoadHostKey(filepath.Join(root, "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, root: root, dir: dir, db: db, hostKey: hk,
		panel:   &panel{perms: allPerms},
		servers: &servers{dir: dir},
	}
	e.srv = New(Options{
		HostKey: hk, Auth: e.panel, Servers: e.servers, Events: events.New(db),
		UID: os.Getuid(), GID: os.Getgid(),
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.addr = ln.Addr().String()
	go func() { _ = e.srv.Serve(ln) }()
	t.Cleanup(func() { _ = e.srv.Close() })
	return e
}

func (e *env) dial(user string, auth ...ssh.AuthMethod) (*sftp.Client, error) {
	c, err := ssh.Dial("tcp", e.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.FixedHostKey(e.hostKey.PublicKey()),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	cl, err := sftp.NewClient(c)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	e.t.Cleanup(func() { _ = cl.Close(); _ = c.Close() })
	return cl, nil
}

func (e *env) login() *sftp.Client {
	e.t.Helper()
	cl, err := e.dial("alice."+srvID, ssh.Password("hunter2"))
	if err != nil {
		e.t.Fatal(err)
	}
	return cl
}

func newKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func put(t *testing.T, cl *sftp.Client, name, data string) {
	t.Helper()
	f, err := cl.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := f.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, cl *sftp.Client, name string) string {
	t.Helper()
	f, err := cl.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFiles(t *testing.T) {
	e := newEnv(t)
	cl := e.login()

	if err := cl.Mkdir("/plugins"); err != nil {
		t.Fatal(err)
	}
	put(t, cl, "/plugins/a.yml", "hello")
	if got := get(t, cl, "plugins/a.yml"); got != "hello" {
		t.Fatalf("read back %q", got)
	}
	// Created with explicit modes, whatever the umask.
	for name, want := range map[string]fs.FileMode{"plugins": 0o755, "plugins/a.yml": 0o644} {
		fi, err := os.Stat(filepath.Join(e.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", name, fi.Mode().Perm(), want)
		}
	}

	entries, err := cl.ReadDir("/plugins")
	if err != nil || len(entries) != 1 || entries[0].Name() != "a.yml" {
		t.Fatalf("list: %v %v", entries, err)
	}

	// Plain rename never replaces; posix-rename does.
	put(t, cl, "/b.yml", "other")
	if err := cl.Rename("/b.yml", "/plugins/a.yml"); err == nil {
		t.Fatal("rename replaced an existing file")
	}
	if err := cl.PosixRename("/b.yml", "/plugins/a.yml"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, cl, "/plugins/a.yml"); got != "other" {
		t.Fatalf("after posix-rename: %q", got)
	}

	// Only permission bits can be set.
	if err := cl.Chmod("/plugins/a.yml", 0o4755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(e.dir, "plugins/a.yml"))
	if fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		t.Fatalf("setuid bit was set: %v", fi.Mode())
	}
	// Ownership changes are ignored, not refused.
	if err := cl.Chown("/plugins/a.yml", 0, 0); err != nil {
		t.Fatal(err)
	}

	if err := cl.Truncate("/plugins/a.yml", 2); err != nil {
		t.Fatal(err)
	}
	if got := get(t, cl, "/plugins/a.yml"); got != "ot" {
		t.Fatalf("after truncate: %q", got)
	}

	if err := cl.Symlink("a.yml", "/plugins/link.yml"); err != nil {
		t.Fatal(err)
	}
	if target, err := cl.ReadLink("/plugins/link.yml"); err != nil || target != "a.yml" {
		t.Fatalf("readlink: %q %v", target, err)
	}
	if got := get(t, cl, "/plugins/link.yml"); got != "ot" {
		t.Fatalf("through the link: %q", got)
	}

	if err := cl.Remove("/plugins"); err == nil {
		t.Fatal("remove deleted a directory")
	}
	if err := cl.RemoveDirectory("/plugins/a.yml"); err == nil {
		t.Fatal("rmdir deleted a file")
	}
	for _, name := range []string{"/plugins/a.yml", "/plugins/link.yml"} {
		if err := cl.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := cl.RemoveDirectory("/plugins"); err != nil {
		t.Fatal(err)
	}

	if wd, err := cl.Getwd(); err != nil || wd != "/" {
		t.Fatalf("getwd %q %v", wd, err)
	}
}

// Nothing outside the server's directory can be read or changed: not with
// "..", absolute paths, or symlinks a hostile server planted.
func TestConfinement(t *testing.T) {
	e := newEnv(t)
	cl := e.login()

	secret := filepath.Join(e.root, "secret")
	if err := os.WriteFile(secret, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"abs": secret, "rel": "../secret", "dir": e.root, "etc": "/etc",
	} {
		if err := os.Symlink(target, filepath.Join(e.dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	// ".." never climbs above the root: it's resolved from "/".
	put(t, cl, "../../escape", "x")
	if _, err := os.Stat(filepath.Join(e.dir, "escape")); err != nil {
		t.Fatalf("../../escape should land in the server's directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "escape")); err == nil {
		t.Fatal("wrote outside the server's directory")
	}

	for _, name := range []string{"/abs", "/rel", "/dir/secret", "/etc/passwd"} {
		if _, err := cl.Open(name); err == nil {
			t.Errorf("read %s through a symlink out of the directory", name)
		}
		if f, err := cl.Create(name); err == nil {
			_ = f.Close()
			t.Errorf("wrote %s through a symlink out of the directory", name)
		}
		if _, err := cl.ReadDir(name); err == nil && name != "/abs" && name != "/rel" {
			t.Errorf("listed %s through a symlink out of the directory", name)
		}
		if err := cl.Chmod(name, 0o777); err == nil {
			t.Errorf("chmod %s through a symlink out of the directory", name)
		}
		if err := cl.Truncate(name, 0); err == nil {
			t.Errorf("truncated %s through a symlink out of the directory", name)
		}
	}
	if err := cl.Rename("/escape", "/dir/moved"); err == nil {
		t.Error("renamed a file out of the directory")
	}
	if err := cl.Mkdir("/dir/new"); err == nil {
		t.Error("made a directory out of the directory")
	}
	if err := cl.Remove("/"); err == nil {
		t.Error("removed the server's directory")
	}
	if err := cl.Rename("/", "/x"); err == nil {
		t.Error("renamed the server's directory")
	}
	// The link itself can go.
	if err := cl.Remove("/abs"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "do-not-touch" {
		t.Fatalf("the file outside changed: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "moved")); err == nil {
		t.Fatal("a file was moved out")
	}
}

// A FIFO (or device node) planted in the directory is refused, and never
// blocks the session.
func TestSpecialFiles(t *testing.T) {
	e := newEnv(t)
	if err := syscall.Mkfifo(filepath.Join(e.dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("fifo", filepath.Join(e.dir, "to-fifo")); err != nil {
		t.Fatal(err)
	}
	cl := e.login()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, name := range []string{"/fifo", "/to-fifo"} {
			if _, err := cl.Open(name); err == nil {
				t.Errorf("opened %s", name)
			}
			if _, err := cl.OpenFile(name, os.O_WRONLY); err == nil {
				t.Errorf("opened %s for writing", name)
			}
			if _, err := cl.ReadDir(name); err == nil {
				t.Errorf("listed %s", name)
			}
		}
		// Still listable and removable.
		if _, err := cl.Stat("/fifo"); err != nil {
			t.Error(err)
		}
		if err := cl.Remove("/fifo"); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a FIFO blocked the session")
	}
}

// The egg's file_denylist applies to SFTP as to the web file manager:
// denied files are listed but can't be read, written, moved, or deleted,
// under their own name or through a link.
func TestDenylist(t *testing.T) {
	e := newEnv(t)
	e.servers.deny = []string{"server.jar", "secrets/"}
	for name, data := range map[string]string{"server.jar": "jar", "secrets/token": "t", "ok.txt": "ok"} {
		p := filepath.Join(e.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cl := e.login()
	if err := cl.Symlink("secrets/token", "/link"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/server.jar", "/secrets/token", "/link"} {
		if _, err := cl.Open(name); err == nil {
			t.Errorf("read %s", name)
		}
		if _, err := cl.OpenFile(name, os.O_WRONLY|os.O_TRUNC); err == nil {
			t.Errorf("opened %s for writing", name)
		}
	}
	if err := cl.Remove("/server.jar"); err == nil {
		t.Error("removed server.jar")
	}
	if err := cl.Rename("/secrets", "/open"); err == nil {
		t.Error("moved the denied directory")
	}
	if err := cl.Rename("/ok.txt", "/server.jar2"); err != nil {
		t.Error(err)
	}
	if err := cl.PosixRename("/server.jar2", "/server.jar"); err == nil {
		t.Error("replaced server.jar")
	}
	if err := cl.Chmod("/server.jar", 0o777); err == nil {
		t.Error("changed server.jar's mode")
	}
	if _, err := cl.Create("/secrets/new"); err == nil {
		t.Error("created a file in the denied directory")
	}
	// Listed, and the files are untouched.
	if _, err := cl.Stat("/server.jar"); err != nil {
		t.Error(err)
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, "server.jar")); err != nil || string(b) != "jar" {
		t.Errorf("server.jar = %q, %v", b, err)
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	put(t, e.login(), "/server.properties", "motd=hi")

	e.panel.set(func(p *panel) { p.perms = []string{PermSFTP, PermRead} })
	cl := e.login()
	if got := get(t, cl, "/server.properties"); got != "motd=hi" {
		t.Fatalf("read %q", got)
	}
	if _, err := cl.Create("/new"); err == nil {
		t.Error("created a file without files.write")
	}
	if err := cl.Remove("/server.properties"); err == nil {
		t.Error("removed a file without files.write")
	}
	if err := cl.Mkdir("/d"); err == nil {
		t.Error("made a directory without files.write")
	}

	e.panel.set(func(p *panel) { p.perms = []string{PermSFTP, PermWrite} })
	cl = e.login()
	if _, err := cl.Open("/server.properties"); err == nil {
		t.Error("read a file without files.read")
	}
	if _, err := cl.ReadDir("/"); err == nil {
		t.Error("listed without files.read")
	}

	e.panel.set(func(p *panel) { p.perms = []string{PermRead, PermWrite} })
	if _, err := e.dial("alice."+srvID, ssh.Password("hunter2")); err == nil {
		t.Error("logged in without the sftp permission")
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	for _, user := range []string{"alice." + srvID, "alice." + srvID[len(srvID)-8:]} {
		if _, err := e.dial(user, ssh.Password("hunter2")); err != nil {
			t.Errorf("%s: %v", user, err)
		}
	}
	for user, pw := range map[string]string{
		"alice." + srvID:       "wrong",
		"bob." + srvID:         "hunter2",
		"alice":                "hunter2",
		"alice.":               "hunter2",
		"alice.deadbeef":       "hunter2",
		".0b6f7a3e":            "hunter2",
		"alice.0b6f7a3e.extra": "hunter2",
	} {
		if _, err := e.dial(user, ssh.Password(pw)); err == nil {
			t.Errorf("%s/%s logged in", user, pw)
		}
	}
	// Usernames with dots split at the last one.
	if u, s, ok := parseUsername("first.last." + srvID); !ok || u != "first.last" || s != srvID {
		t.Errorf("parseUsername: %q %q %v", u, s, ok)
	}

	// Logins are recorded for the Panel.
	evs, err := events.New(e.db).Since(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Type == EventLogin && ev.ServerID == srvID && ev.Data["user"] == "alice" && ev.Data["method"] == "password" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("recorded %d logins, want 2", n)
	}
}

// Keys aren't accepted, and passwords don't work while the Panel is
// unreachable: nothing is cached.
func TestNoKeysNoCache(t *testing.T) {
	e := newEnv(t)
	if _, err := e.dial("alice."+srvID, ssh.PublicKeys(newKey(t))); err == nil {
		t.Error("a key logged in")
	}
	e.login()
	e.panel.set(func(p *panel) { p.down = true })
	if _, err := e.dial("alice."+srvID, ssh.Password("hunter2")); err == nil {
		t.Error("a password logged in with the Panel down")
	}
}

// Deleting a server or starting an install over it ends its sessions, and
// while it's installing, requests fail with the reason.
func TestServerUnavailable(t *testing.T) {
	e := newEnv(t)
	cl := e.login()
	put(t, cl, "/a", "x")

	e.servers.mu.Lock()
	e.servers.err = errors.New("server is installing")
	e.servers.mu.Unlock()
	_, err := cl.Open("/a")
	if err == nil || !strings.Contains(err.Error(), "installing") {
		t.Fatalf("open while installing: %v", err)
	}
	e.servers.mu.Lock()
	e.servers.err = nil
	e.servers.mu.Unlock()

	if n := e.srv.Disconnect(srvID); n != 1 {
		t.Fatalf("disconnected %d connections, want 1", n)
	}
	if _, err := cl.Stat("/a"); err == nil {
		t.Fatal("the session survived Disconnect")
	}
}

// Over the soft disk limit, nothing that adds data works, but files can
// still be read, moved, and deleted to get back under.
func TestOverDiskLimit(t *testing.T) {
	e := newEnv(t)
	cl := e.login()
	put(t, cl, "/world.dat", "big")
	if err := cl.Mkdir("/logs"); err != nil {
		t.Fatal(err)
	}
	e.servers.mu.Lock()
	e.servers.errWrite = errors.New("server is over its disk limit")
	e.servers.mu.Unlock()

	if _, err := cl.Create("/more"); err == nil || !strings.Contains(err.Error(), "disk limit") {
		t.Errorf("create over the limit: %v", err)
	}
	if _, err := cl.OpenFile("/world.dat", os.O_WRONLY); err == nil {
		t.Error("opened a file for writing over the limit")
	}
	if err := cl.Mkdir("/d"); err == nil {
		t.Error("made a directory over the limit")
	}
	if got := get(t, cl, "/world.dat"); got != "big" {
		t.Errorf("read %q", got)
	}
	if err := cl.Rename("/world.dat", "/logs/world.dat"); err != nil {
		t.Errorf("rename over the limit: %v", err)
	}
	if err := cl.Truncate("/logs/world.dat", 0); err != nil {
		t.Errorf("truncate over the limit: %v", err)
	}
	if err := cl.Remove("/logs/world.dat"); err != nil {
		t.Errorf("remove over the limit: %v", err)
	}
	if err := cl.RemoveDirectory("/logs"); err != nil {
		t.Errorf("rmdir over the limit: %v", err)
	}
}

func TestFailureLimit(t *testing.T) {
	s := New(Options{HostKey: newKey(t)})
	addr := mustAddr(t, "203.0.113.7")
	for range maxFailures {
		s.failed(addr)
	}
	if s.admit(&conn{}, addr) {
		t.Fatal("an address over the failure limit was admitted")
	}
	if !s.admit(&conn{}, mustAddr(t, "203.0.113.8")) {
		t.Fatal("another address was refused")
	}
	s.failures[addr].since = time.Now().Add(-failWindow - time.Second)
	if !s.admit(&conn{}, addr) {
		t.Fatal("the limit didn't end with its window")
	}
}

func TestHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "host")
	a, err := LoadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(a) != Fingerprint(b) || !strings.HasPrefix(Fingerprint(a), "SHA256:") {
		t.Fatalf("fingerprints %s %s", Fingerprint(a), Fingerprint(b))
	}
	if a.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("key type %s", a.PublicKey().Type())
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", fi, err)
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A temporary password's sessions end when it runs out, or when the Panel
// says it was revoked.
func TestPasswordExpiry(t *testing.T) {
	e := newEnv(t)
	e.panel.set(func(p *panel) { p.expires = time.Now().Add(300 * time.Millisecond) })
	cl := e.login()
	if _, err := cl.ReadDir("/"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := cl.ReadDir("/"); err == nil {
		t.Error("the session outlived its password")
	}

	e.panel.set(func(p *panel) { p.expires = time.Time{} })
	cl = e.login()
	if n := e.srv.DisconnectLogin(srvID, "bob"); n != 0 {
		t.Errorf("disconnected %d of bob's", n)
	}
	if n := e.srv.DisconnectLogin(srvID, "alice"); n != 1 {
		t.Errorf("disconnected %d of alice's, want 1", n)
	}
	if _, err := cl.ReadDir("/"); err == nil {
		t.Error("the session outlived its revoked password")
	}
}
