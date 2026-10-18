package files

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// sftpHost is an SFTP server on the loopback serving dir, for user with
// password.
func sftpHost(t *testing.T, dir, user, password string) (port int, hostKey string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == user && string(pw) == password {
			return nil, nil
		}
		return nil, errors.New("no")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, requests, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range requests {
							_ = r.Reply(r.Type == "subsystem" && string(r.Payload[4:]) == "sftp", nil)
						}
					}()
					srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(dir))
					if err != nil {
						continue
					}
					go func() { _ = srv.Serve(); _ = ch.Close() }()
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}

func TestImport(t *testing.T) {
	e := newEnv(t)
	old := t.TempDir()
	for name, data := range map[string]string{
		"server.properties":    "motd=old",
		"world/level.dat":      "level",
		"plugins/a.jar":        "a",
		"plugins/b/config.yml": "b",
		"keep.txt":             "theirs",
	} {
		p := filepath.Join(old, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(old, "passwd")); err != nil {
		t.Fatal(err)
	}
	port, key := sftpHost(t, old, "steve", "hunter2")
	e.write("keep.txt", "ours")

	// The loopback isn't the public internet.
	if _, err := e.svc.ImportHostKey(ctx, "127.0.0.1", port); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("host key from the loopback: %v", err)
	}
	src := ImportSource{Host: "localhost", Port: port, User: "steve", Password: "hunter2", HostKey: key, Path: old}
	id, err := e.svc.Import(ctx, srvID, "u1", src, "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "public internet") {
		t.Fatalf("an import from the loopback: %s %q", j.Status, j.Error)
	}
	e.svc.o.PullAllowed = func(ip netip.Addr) bool { return ip.IsLoopback() }

	got, err := e.svc.ImportHostKey(ctx, "127.0.0.1", port)
	if err != nil || got["key"] != key || !strings.HasPrefix(got["fingerprint"], "SHA256:") {
		t.Fatalf("host key: %v, %v", got, err)
	}

	// A wrong password, and a host key that isn't the host's.
	bad := src
	bad.Password = "nope"
	id, _ = e.svc.Import(ctx, srvID, "u1", bad, "")
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "refused") {
		t.Errorf("a wrong password: %s %q", j.Status, j.Error)
	}
	_, other := sftpHost(t, old, "x", "y")
	bad = src
	bad.HostKey = other
	id, _ = e.svc.Import(ctx, srvID, "u1", bad, "")
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "isn't the one you confirmed") {
		t.Errorf("another host's key: %s %q", j.Status, j.Error)
	}

	e.servers.mu.Lock()
	e.servers.deny = []string{"plugins/b/"}
	e.servers.mu.Unlock()
	id, err = e.svc.Import(ctx, srvID, "u1", src, "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(id); j.Status != jobs.Succeeded {
		t.Fatalf("import: %s %q", j.Status, j.Error)
	}
	for name, want := range map[string]string{
		"server.properties": "motd=old", "world/level.dat": "level", "plugins/a.jar": "a",
		"keep.txt": "ours", // never replaced
	} {
		if got := e.read(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if e.exists("passwd") || e.exists("plugins/b/config.yml") {
		t.Error("a link or a denied file was imported")
	}
	st, err := e.svc.ImportStatus(ctx, srvID, id)
	if err != nil || st.Status != jobs.Succeeded || st.Files != 3 || st.Skipped < 3 {
		t.Errorf("status: %+v, %v", st, err)
	}

	// Checked before it starts.
	for _, s := range []ImportSource{
		{Host: "", User: "u", Password: "p", HostKey: key},
		{Host: "h", Port: 70000, User: "u", Password: "p", HostKey: key},
		{Host: "h", User: "", Password: "p", HostKey: key},
		{Host: "h", User: "u", HostKey: key},
		{Host: "h", User: "u", Password: "p"},
		{Host: "h", User: "u", PrivateKey: "not a key", HostKey: key},
		{Host: "10.0.0.2", User: "u", Password: "p", HostKey: key},
	} {
		e.svc.o.PullAllowed = nil
		if _, err := e.svc.Import(ctx, srvID, "u1", s, ""); err == nil {
			t.Errorf("%+v was taken", s)
		}
	}
}
