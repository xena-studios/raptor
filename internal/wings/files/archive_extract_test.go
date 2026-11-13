package files

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testExtractor(f *FS) *extractor {
	return &extractor{f: f, ctx: context.Background(), dest: ".", space: &spaceLeft{limit: -1}, res: &ArchiveResult{}, dirs: map[string]bool{}}
}

func text(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

// The directories an extraction remembers are names only: one removed
// since is made again, and one swapped for a link out can't be written
// through.
func TestExtractStaleDirs(t *testing.T) {
	f, dir := testFS(t)
	outside := t.TempDir()
	x := testExtractor(f)
	if err := x.entry("d/a", kindFile, 0, "", text("a"), false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	if err := x.entry("d/b", kindFile, 0, "", text("b"), false, time.Time{}); err != nil {
		t.Fatalf("after its directory was removed: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "d/b")); string(b) != "b" {
		t.Errorf("d/b = %q", b)
	}

	if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	if err := x.entry("d/c", kindFile, 0, "", text("c"), false, time.Time{}); err == nil {
		t.Error("wrote through a directory swapped for a link out")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside: %v", entries)
	}
}

// What's in a new file's way: a link is replaced, never written through; a
// directory, a FIFO (never opened, so nothing blocks), and a denied file
// are left; an existing file is replaced in place.
func TestExtractInTheWay(t *testing.T) {
	f, dir := testFS(t)
	outside := t.TempDir()
	f.deny = Compile([]string{"denied"})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(filepath.Join(outside, "x"), filepath.Join(dir, "link-out")))
	must(os.Symlink("../escape", filepath.Join(dir, "link-up")))
	must(os.Mkdir(filepath.Join(dir, "a-dir"), 0o755))
	must(syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "denied"), []byte("keep"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "old"), []byte("old contents"), 0o600))
	must(os.Link(filepath.Join(dir, "old"), filepath.Join(dir, "old-hardlink")))

	x := testExtractor(f)
	for _, name := range []string{"link-out", "link-up", "a-dir", "fifo", "denied", "old"} {
		done := make(chan error, 1)
		go func() { done <- x.entry(name, kindFile, 0o640, "", text("new"), false, time.Time{}) }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: blocked", name)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside: %v", entries)
	}
	for name, want := range map[string]string{"link-out": "new", "link-up": "new", "denied": "keep", "old": "new", "old-hardlink": "new"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s: %v %v", name, fi, err)
			continue
		}
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
	if fi, _ := os.Lstat(filepath.Join(dir, "a-dir")); !fi.IsDir() {
		t.Error("a directory was replaced")
	}
	if fi, _ := os.Lstat(filepath.Join(dir, "fifo")); fi.Mode()&os.ModeNamedPipe == 0 {
		t.Error("the FIFO was replaced")
	}
	if x.res.Files != 3 || x.res.Skipped != 3 {
		t.Errorf("result = %+v", *x.res)
	}
}

// Extracted files get the archive's permission bits (never setuid), or
// FileMode, the archive's times, and the servers' user, whether new or
// replacing a file.
func TestExtractModesAndTimes(t *testing.T) {
	f, dir := testFS(t)
	mtime := time.Date(2020, 5, 6, 7, 8, 9, 123456789, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := makeTar(t, []tarEntry{
		{name: "exec", data: "x", typ: tar.TypeReg, mode: 0o4755},
		{name: "private", data: "x", typ: tar.TypeReg, mode: 0o600},
		{name: "existing", data: "y", typ: tar.TypeReg, mode: 0o640},
	})
	if err := os.WriteFile(filepath.Join(dir, "a.tar"), setTarTimes(t, archive, mtime), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Extract(context.Background(), "a.tar", "", -1, ExtractOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Extract(context.Background(), "single.gz", "", -1, ExtractOptions{}); err == nil {
		t.Fatal("a missing archive was extracted")
	}
	for name, mode := range map[string]os.FileMode{"exec": 0o755, "private": 0o600, "existing": 0o640} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode() != mode {
			t.Errorf("%s: mode %v, want %v", name, fi.Mode(), mode)
		}
		if d := fi.ModTime().Sub(mtime).Abs(); d >= time.Microsecond { // futimes off Linux keeps µs
			t.Errorf("%s: mtime %v, want %v", name, fi.ModTime(), mtime)
		}
		if st := fi.Sys().(*syscall.Stat_t); int(st.Uid) != os.Getuid() {
			t.Errorf("%s: owner %d", name, st.Uid)
		}
	}
}

// setTarTimes rewrites a tar's modification times (PAX, so nanoseconds
// are kept).
func setTarTimes(t *testing.T, archive []byte, mtime time.Time) []byte {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(string(archive)))
	var out strings.Builder
	tw := tar.NewWriter(&out)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		h.ModTime, h.Format = mtime, tar.FormatPAX
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(out.String())
}
