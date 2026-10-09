//go:build e2e

package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/files"
	wsftp "github.com/xena-studios/raptor/internal/wings/sftp"
)

// Symlink and path-traversal suite (docs/SECURITY-MODEL.md#server-files).
// A hostile egg's install script runs as root in the install container and
// plants every trick it can in the server directory; the server's own process
// could plant the same. Then Wings does everything it does with a server's
// files: install (ownership fix), config file edits on start, disk usage,
// reinstall, and delete. Nothing outside the server directory may change,
// and nothing may hang.
func TestPathSafety(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	defer m.Close()

	// Things a hostile server must not touch: a root-only file on the host,
	// another server's files, and system files.
	sentinelDir := filepath.Join(e.dir, "host-secrets")
	if err := os.Mkdir(sentinelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sentinelDir, "secret.properties")
	if err := os.WriteFile(sentinel, []byte("server-port=1\nsecret=do-not-touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := e.create(m, freePort(t))
	victimDir := filepath.Join(e.opts.VolumesDir, victim)
	victimFile := filepath.Join(victimDir, "server.properties")
	if err := os.WriteFile(victimFile, []byte("server-port=2\nowner=victim\n"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	// The directories links point at too: an ownership change through a
	// link would chown them, not the files in them.
	guarded := []string{sentinel, victimFile, "/etc/passwd", "/etc/hosts", "/etc/hostname", sentinelDir, victimDir, "/etc", "/usr"}
	before := snapshot(t, guarded)

	id := e.create(m, freePort(t), func(c *Config) {
		c.Egg = hostileEgg(sentinel, victimFile, sentinelDir)
	})
	dir := filepath.Join(e.opts.VolumesDir, id)
	check := func(step string) {
		t.Helper()
		if after := snapshot(t, guarded); after != before {
			t.Fatalf("after %s, a file outside the server directory changed:\nbefore: %s\nafter:  %s", step, before, after)
		}
	}
	check("install (the ownership fix)")

	// Start: config file edits run over every planted file. It must not hang
	// (a FIFO would block a naive open) and must still start the server.
	done := make(chan error, 1)
	go func() { done <- m.Start(context.Background(), id) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start: %v", err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("start hung on the planted files")
	}
	waitState(t, m, id, Starting, 30*time.Second)
	check("config file edits on start")

	// The edits that stay inside the directory still happen: through an
	// internal symlink (the link kept), and for paths the egg tried to point
	// outside, which land inside instead.
	if b, err := os.ReadFile(filepath.Join(dir, "real/inner.properties")); err != nil || !strings.Contains(string(b), "edited=yes") {
		t.Errorf("write through an internal symlink: %q, %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "inner.properties")); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the internal symlink was replaced: %v, %v", fi, err)
	}
	for _, p := range []string{"escape.properties", "etc/absolute.properties"} {
		if b, err := os.ReadFile(filepath.Join(dir, p)); err != nil || !strings.Contains(string(b), "edited=yes") {
			t.Errorf("%s (an egg path that tried to leave the directory) wasn't written inside it: %q, %v", p, b, err)
		}
	}
	// Each refused file is reported on the console, not silently skipped.
	st, _ := m.Status(id)
	hist := strings.Join(st.Console.History(), "\n")
	for _, name := range []string{"passwd.properties", "etc-link/hosts", "sibling.properties", "sentinel.properties", "loop.properties", "fifo.properties"} {
		if !strings.Contains(hist, name) {
			t.Errorf("no console notice for the refused %s:\n%s", name, hist)
		}
	}

	// Disk usage doesn't follow symlinks (one points at the whole of /usr).
	u, err := m.DiskUsage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes > 64<<20 {
		t.Errorf("disk usage %d MiB: it followed a symlink out of the directory", u.Bytes>>20)
	}

	sftpAttack(t, m, id)
	check("sftp")
	fileManagerAttack(t, m, id, sentinelDir)
	check("web file manager")

	if err := m.Kill(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	job, err := m.Install(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.o.Jobs.Wait(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Offline, 2*time.Minute)
	check("reinstall")

	if err := m.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("server directory still there: %v", err)
	}
	check("delete")
	if _, err := os.Stat(sentinelDir); err != nil {
		t.Errorf("delete removed a symlinked directory's target: %v", err)
	}
	// The victim server is intact and still starts.
	ready(t, m, victim)
}

// sftpAttack goes after every planted file over SFTP, as a user with full
// file permissions. Reads and writes through links out of the directory,
// and opening the FIFO, must fail without hanging; the internal link works.
func sftpAttack(t *testing.T, m *Manager, id string) {
	t.Helper()
	hk, err := wsftp.LoadHostKey(filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	srv := wsftp.New(wsftp.Options{HostKey: hk, Auth: allowAll{}, Servers: m, UID: m.o.UID, GID: m.o.GID})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	c, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
		User: "owner." + id[len(id)-8:], Auth: []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.FixedHostKey(hk.PublicKey()), Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	cl, err := sftp.NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, name := range []string{
			"passwd.properties", "relative.properties", "sentinel.properties", "sibling.properties",
			"etc-link/hosts", "secrets-dir/secret.properties", "loop.properties", "fifo.properties",
			"../../../etc/hosts", "/etc/hostname",
		} {
			if f, err := cl.Open(name); err == nil {
				_ = f.Close()
				t.Errorf("sftp read %s", name)
			}
			if f, err := cl.OpenFile(name, os.O_WRONLY|os.O_TRUNC); err == nil {
				_ = f.Close()
				t.Errorf("sftp opened %s for writing", name)
			}
			_ = cl.Chmod(name, 0o777)
			_ = cl.Truncate(name, 0)
		}
		_ = cl.Rename("real/inner.properties", "secrets-dir/moved")
		_ = cl.Mkdir("secrets-dir/new")
		f, err := cl.OpenFile("inner.properties", os.O_WRONLY|os.O_APPEND)
		if err != nil {
			t.Errorf("sftp write through an internal symlink: %v", err)
			return
		}
		_ = f.Close()
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("sftp hung on the planted files")
	}
}

// fileManagerAttack does the same through the web file manager's
// operations, and archives: compressing must not pull in what links point
// at, and extracting must not write through them.
func fileManagerAttack(t *testing.T, m *Manager, id, sentinelDir string) {
	t.Helper()
	ctx := context.Background()
	svc := files.NewService(files.Options{Servers: m, Store: m.o.Store, UID: m.o.UID, GID: m.o.GID})
	hostile := []string{
		"passwd.properties", "relative.properties", "sentinel.properties", "sibling.properties",
		"etc-link/hosts", "secrets-dir/secret.properties", "loop.properties", "fifo.properties",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Paths that try to leave the directory land inside it.
		for _, name := range []string{"../../../etc/raptor-escape", "/etc/raptor-absolute"} {
			if err := svc.Write(ctx, id, name, []byte("inside")); err != nil {
				t.Errorf("file manager write %s: %v", name, err)
			}
			if b, err := os.ReadFile(filepath.Join(m.o.VolumesDir, id, "etc", filepath.Base(name))); err != nil || string(b) != "inside" {
				t.Errorf("%s wasn't written inside the directory: %q, %v", name, b, err)
			}
		}
		for _, name := range hostile {
			if _, err := svc.Read(ctx, id, name); err == nil {
				t.Errorf("file manager read %s", name)
			}
			if err := svc.Write(ctx, id, name, []byte("pwned")); err == nil {
				t.Errorf("file manager wrote %s", name)
			}
			if _, err := svc.StartDownload(ctx, id, name); err == nil {
				t.Errorf("file manager downloaded %s", name)
			}
			if _, err := svc.StartUpload(ctx, id, "u", name, 5); err == nil {
				t.Errorf("file manager started an upload to %s", name)
			}
			_, _ = svc.Copy(ctx, id, name)
			_ = svc.Chmod(ctx, id, "/", []files.Chmod{{Name: name, Mode: 0o777}})
		}
		_ = svc.Mkdir(ctx, id, "etc-link/new")
		_ = svc.Rename(ctx, id, "/", []files.Move{{From: "real/inner.properties", To: "secrets-dir/moved"}})
		if _, err := svc.List(ctx, id, "etc-link"); err == nil {
			t.Error("file manager listed a directory outside")
		}
		if err := svc.Write(ctx, id, "inner.properties", []byte("edited=yes\n")); err != nil {
			t.Errorf("file manager write through an internal symlink: %v", err)
		}

		fsys, err := files.OpenServer(m, id, m.o.UID, m.o.GID)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = fsys.Close() }()
		entries, err := fsys.List("/")
		if err != nil {
			t.Error(err)
			return
		}
		var names []string
		for _, fi := range entries {
			names = append(names, fi.Name())
		}
		res, err := fsys.Compress(ctx, "/", names, -1, time.Now(), files.CompressOptions{})
		if err != nil {
			t.Errorf("compress: %v", err)
			return
		}
		archive, err := os.ReadFile(filepath.Join(m.o.VolumesDir, id, res.Archive))
		if err != nil {
			t.Error(err)
			return
		}
		if len(archive) > 1<<20 {
			t.Errorf("the archive is %d KiB: it followed a link out", len(archive)>>10)
		}
		zr, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			t.Error(err)
			return
		}
		content, _ := io.ReadAll(zr)
		for _, leak := range []string{"do-not-touch", "owner=victim", "root:x:0:0"} {
			if bytes.Contains(content, []byte(leak)) {
				t.Errorf("the archive holds %q from outside the directory", leak)
			}
		}

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, name := range []string{"etc-link/raptor-pwned", "secrets-dir/raptor-pwned", "../../raptor-pwned", "sibling.properties", "passwd.properties"} {
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 5})
			_, _ = tw.Write([]byte("pwned"))
		}
		_ = tw.Close()
		if err := os.WriteFile(filepath.Join(m.o.VolumesDir, id, "evil.tar"), buf.Bytes(), 0o644); err != nil { //nolint:gosec // test file
			t.Error(err)
			return
		}
		_, _ = fsys.Extract(ctx, "evil.tar", "", -1)
		for _, p := range []string{"/etc/raptor-pwned", "/etc/raptor-escape", "/etc/raptor-absolute", filepath.Join(sentinelDir, "raptor-pwned")} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("extracting wrote %s", p)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("the file manager hung on the planted files")
	}
}

type allowAll struct{}

func (allowAll) Password(context.Context, wsftp.Login, string) (wsftp.Grant, error) {
	return wsftp.Grant{Permissions: []string{wsftp.PermSFTP, wsftp.PermRead, wsftp.PermWrite}}, nil
}

func (allowAll) PublicKey(context.Context, wsftp.Login, ssh.PublicKey) (wsftp.Grant, error) {
	return wsftp.Grant{}, wsftp.ErrDenied
}

// hostileEgg plants symlinks, a loop, a FIFO, and hard links, and points
// its config file rules at them and at paths that try to leave the server
// directory.
func hostileEgg(sentinel, victimFile, sentinelDir string) []byte {
	install := fmt.Sprintf(`set -e
cd /mnt/server
# Symlinks out of the directory: absolute, relative, to another server, to
# a root-only host file, and to a directory.
ln -s /etc/passwd passwd.properties
ln -s ../../../../../../../../etc/hosts relative.properties
ln -s %[1]s sentinel.properties
ln -s %[2]s sibling.properties
ln -s /etc etc-link
ln -s %[3]s secrets-dir
ln -s /usr big-dir
# A loop, a dangling link, and a FIFO.
ln -s loop.properties loop.properties
ln -s does-not-exist dangling.properties
mkfifo fifo.properties
# An internal symlink (legitimate: written through) and a hard link.
mkdir -p real
echo edited=no > real/inner.properties
ln -s real/inner.properties inner.properties
ln real/inner.properties hardlink.properties
# Root-owned, setuid, and unreadable files the ownership fix walks over.
touch setuid && chmod 4755 setuid
mkdir -p locked && chmod 000 locked
echo planted`, sentinel, victimFile, sentinelDir)
	files := map[string]any{}
	for _, p := range []string{
		"passwd.properties", "relative.properties", "sentinel.properties", "sibling.properties",
		"etc-link/hosts", "secrets-dir/secret.properties", "loop.properties", "dangling.properties",
		"fifo.properties", "inner.properties", "hardlink.properties",
		"../../escape.properties", "/etc/absolute.properties",
	} {
		files[p] = map[string]any{"parser": "properties", "find": map[string]string{"edited": "yes", "server-port": "{{server.build.default.port}}"}}
	}
	return fmt.Appendf(nil, `{
		"meta": {"version": "PTDL_v2"},
		"name": "Hostile",
		"docker_images": {"BusyBox": "busybox:1"},
		"startup": "sh",
		"config": {"files": %q, "startup": "{\"done\": \"READY\"}", "stop": "exit"},
		"scripts": {"installation": {"script": %q, "container": "busybox:1", "entrypoint": "sh"}}
	}`, mustJSON(files), install)
}

// snapshot fingerprints files (content, mode, and owner) and directories
// (mode and owner).
func snapshot(t *testing.T, paths []string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		var data []byte // directories: mode and owner only
		if !fi.IsDir() {
			if data, err = os.ReadFile(p); err != nil { //nolint:gosec // test paths
				t.Fatalf("snapshot %s: %v", p, err)
			}
		}
		st := fi.Sys().(*syscall.Stat_t)
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s %s %v %d:%d; ", p, hex.EncodeToString(sum[:8]), fi.Mode(), st.Uid, st.Gid)
	}
	return b.String()
}
