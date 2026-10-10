package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/webdav"
)

// sftpServer is an SSH server with the SFTP subsystem, signing in with a
// password, serving the real filesystem.
func sftpServer(t *testing.T, password string) (host string, port int, hostKey string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if string(p) != password {
			return nil, ssh.ErrNoAuth
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSFTP(c, cfg)
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}

func serveSFTP(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
				if ok {
					srv, err := sftp.NewServer(ch)
					if err == nil {
						_ = srv.Serve()
					}
					_ = ch.Close()
				}
			}
		}()
	}
}

// A repository over SFTP: tested, backed up to, restored from; and a
// server answering with another host key is refused.
func TestSFTPDestination(t *testing.T) {
	ctx := context.Background()
	host, port, hostKey := sftpServer(t, "hunter2")
	remote := filepath.Join(t.TempDir(), "remote")
	e := newEngine(t)
	e.Dest = Destination{ID: "box", Type: SFTP, Config: Config{SFTP: &SFTPConfig{
		Host: host, Port: port, Username: "raptor", Path: remote, HostKey: hostKey, Password: "hunter2",
	}}}
	if err := e.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	roundTrip(t, e)

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	e.Dest.SFTP.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(otherSigner.PublicKey())))
	if err := e.Test(ctx); err == nil {
		t.Fatal("a different host key was accepted")
	}
	e.Dest.SFTP.HostKey = hostKey
	e.Dest.SFTP.Password = "wrong"
	if err := e.Test(ctx); err == nil {
		t.Fatal("a wrong password was accepted")
	}
}

// A repository on a WebDAV server.
func TestWebDAVDestination(t *testing.T) {
	ctx := context.Background()
	h := &webdav.Handler{FileSystem: webdav.Dir(t.TempDir()), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "raptor" || p != "pw" {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e := newEngine(t)
	e.Dest = Destination{ID: "dav", Type: WebDAV, Config: Config{WebDAV: &WebDAVConfig{URL: srv.URL, Username: "raptor", Password: "pw"}}}
	if err := e.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	roundTrip(t, e)
	e.Dest.WebDAV.Password = "nope"
	short, cancel := context.WithTimeout(ctx, 2*time.Second) // Kopia retries
	defer cancel()
	if err := e.Test(short); err == nil {
		t.Fatal("a wrong password was accepted")
	}
}

// A folder destination: tested, sized, and a path that's a file fails.
func TestFolderDestination(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	e := newEngine(t)
	e.Dest = Destination{ID: "nas", Type: Folder, Config: Config{Folder: &FolderConfig{Path: filepath.Join(dir, "nas")}}, UploadLimit: 50 << 20}
	if err := e.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	roundTrip(t, e)
	if n, err := e.Size(ctx); err != nil || n <= 0 {
		t.Fatalf("size = %d, %v", n, err)
	}
	file := filepath.Join(dir, "file")
	write(t, file, "x", 0o600)
	e.Dest.Folder.Path = file
	if err := e.Test(ctx); err == nil {
		t.Fatal("a file as the folder worked")
	}
}

// roundTrip backs a directory up to e and restores it elsewhere.
func roundTrip(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "server")
	write(t, filepath.Join(src, "world", "level.dat"), "the world", 0o644)
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: os.Getuid(), GID: os.Getgid()}, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "world", "level.dat")); err != nil || string(b) != "the world" {
		t.Fatalf("restored %q, %v", b, err)
	}
}
