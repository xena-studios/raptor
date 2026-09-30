package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	return &Engine{
		Dest:     Destination{ID: "local", Type: Local, Path: filepath.Join(dir, "repo")},
		Password: "test-password",
		StateDir: filepath.Join(dir, "state"),
	}
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // past the umask
		t.Fatal(err)
	}
}

// tree lists a directory as path -> description, without following symlinks.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "-> " + target
		case fi.IsDir():
			out[rel] = "dir " + fi.Mode().Perm().String()
		case !fi.Mode().IsRegular():
			out[rel] = fi.Mode().Type().String()
		default:
			b, _ := os.ReadFile(p)
			out[rel] = fi.Mode().String() + " " + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "server.properties"), "motd=hi", 0o644)
	write(t, filepath.Join(src, "world/region/r.0.0.mca"), strings.Repeat("x", 100_000), 0o600)
	write(t, filepath.Join(src, "start.sh"), "#!/bin/sh", 0o755)
	if err := os.Symlink("world/region", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	// A symlink out of the directory is stored as a link, never followed.
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "host secret", 0o644)
	if err := os.Symlink(outside, filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.SnapshotID == "" || res.Files != 3 || res.Size != int64(len("motd=hi")+100_000+len("#!/bin/sh")) {
		t.Fatalf("result = %+v", res)
	}

	want := tree(t, src)
	delete(want, "pipe") // pipes, sockets, and devices aren't backed up

	// Restoring replaces whatever is there.
	dst := t.TempDir()
	write(t, filepath.Join(dst, "newer-file"), "gone after restore", 0o644)
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err != nil {
		t.Fatal(err)
	}
	got := tree(t, dst)
	if len(got) != len(want) {
		t.Fatalf("restored %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for k := range got {
		if strings.Contains(got[k], "host secret") {
			t.Errorf("%s: a file outside the directory was backed up", k)
		}
	}
}

func TestRestoreDropsSetuid(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "bin"), "x", 0o755|fs.ModeSetuid)
	if fi, _ := os.Stat(filepath.Join(src, "bin")); fi.Mode()&fs.ModeSetuid == 0 {
		t.Skip("can't create setuid files here")
	}
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o755 {
		t.Fatalf("mode = %v, want -rwxr-xr-x", fi.Mode())
	}
}

func TestIgnore(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "keep.txt"), "a", 0o644)
	write(t, filepath.Join(src, "plugins/dynmap/tiles/1.png"), "b", 0o644)
	write(t, filepath.Join(src, "logs/latest.log"), "c", 0o644)
	write(t, filepath.Join(src, ".pteroignore"), "logs/\n", 0o644)
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src, Ignore: []string{"plugins/dynmap/tiles"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err != nil {
		t.Fatal(err)
	}
	got := tree(t, dst)
	for _, p := range []string{"keep.txt", ".pteroignore", "plugins/dynmap"} {
		if _, ok := got[p]; !ok {
			t.Errorf("%s missing: %v", p, got)
		}
	}
	for _, p := range []string{"plugins/dynmap/tiles", "logs"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s was backed up: %v", p, got)
		}
	}
}

func TestRestoreTooLarge(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "big"), strings.Repeat("x", 5000), 0o644)
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	write(t, filepath.Join(dst, "current"), "untouched", 0o644)
	_, err = e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: -1, GID: -1, MaxSize: 4999}, nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "current")); string(b) != "untouched" {
		t.Fatal("a refused restore changed the directory")
	}
}

func TestIncrementalAndDelete(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "world"), string(randomBytes(t, 2<<20)), 0o644) // incompressible
	first, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "new"), "small change", 0o644)
	second, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b2", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Uploaded >= first.Uploaded/10 {
		t.Fatalf("second backup uploaded %d bytes after %d: not incremental", second.Uploaded, first.Uploaded)
	}

	if err := e.Delete(ctx, []string{first.SnapshotID, "does-not-exist"}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: first.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err == nil {
		t.Fatal("restored a deleted backup")
	}
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: second.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWrongPassword(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "f"), "x", 0o644)
	if _, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil); err != nil {
		t.Fatal(err)
	}
	other := *e
	other.Password = "wrong"
	other.StateDir = t.TempDir()
	if _, err := other.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b2", Dir: src}, nil); err == nil {
		t.Fatal("opened the repository with the wrong password")
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// A symlink planted in the server directory before a restore is removed,
// never written through.
func TestRestoreDoesNotFollowPlantedSymlinks(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "server.properties"), "restored", 0o644)
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "passwd")
	write(t, outside, "host file", 0o644)
	dst := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dst, "server.properties")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore(ctx, RestoreRequest{SnapshotID: res.SnapshotID, Dir: dst, UID: -1, GID: -1}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "host file" {
		t.Fatalf("the restore wrote through a symlink: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "server.properties")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("server.properties: %v, %v", fi, err)
	}
}
