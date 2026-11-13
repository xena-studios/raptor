package files

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests for the archive code that runs on several threads. Run with -race.

func testFS(t *testing.T) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	f, err := Open(dir, os.Getuid(), os.Getgid(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, dir
}

func writeRandom(t *testing.T, p string, n int, seed uint64) []byte {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // test data
	b := make([]byte, n)
	for i := range b {
		if i%3 == 0 { // partly compressible
			b[i] = 'a'
		} else {
			b[i] = byte(rng.Uint32())
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// waitGoroutines fails if more goroutines are left than before: an
// abandoned compressor must not leak its threads.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines left, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Blocks compressed on several threads come back as the same bytes, in
// order, from a standard gzip reader, at every level and thread count.
func TestCompressParallelGzip(t *testing.T) {
	f, dir := testFS(t)
	want := map[string][]byte{}
	for i, n := range []int{5<<20 + 17, 3 << 20, 1, 0, 70_000} {
		name := "d/f" + string(rune('a'+i))
		want[name] = writeRandom(t, filepath.Join(dir, name), n, uint64(i))
	}
	for _, level := range []int{1, 6, 9} {
		for _, threads := range []int{1, 2, 4} {
			res, err := f.Compress(context.Background(), ".", []string{"d"}, -1, time.Now(), CompressOptions{Name: "a", Level: level, Threads: threads})
			if err != nil {
				t.Fatal(err)
			}
			got := readTarGz(t, filepath.Join(dir, res.Archive))
			for name, data := range want {
				if string(got[name]) != string(data) {
					t.Errorf("level %d, %d threads: %s has %d bytes, want %d", level, threads, name, len(got[name]), len(data))
				}
			}
			if err := os.Remove(filepath.Join(dir, res.Archive)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func readTarGz(t *testing.T, p string) map[string][]byte {
	t.Helper()
	file, err := os.Open(p) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	zr.Multistream(false) // one gzip member, as single-threaded gzip writes
	out := map[string][]byte{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(h.Name, "/")] = b
	}
	if rest, _ := io.ReadAll(file); len(rest) != 0 {
		t.Errorf("%d bytes after the gzip stream", len(rest))
	}
	return out
}

// Running out of space stops a parallel compression with ErrNoSpace,
// leaves nothing behind, writes nothing past the limit, and stops every
// compressor thread. So does cancelling it.
func TestCompressParallelAbort(t *testing.T) {
	f, dir := testFS(t)
	for i := range 8 {
		writeRandom(t, filepath.Join(dir, "d", "f"+string(rune('a'+i))), 2<<20, uint64(i))
	}
	before := runtime.NumGoroutine()
	for range 5 {
		_, err := f.Compress(context.Background(), ".", []string{"d"}, 3<<20, time.Now(), CompressOptions{Name: "a", Level: 1, Threads: 4})
		if !errors.Is(err, ErrNoSpace) {
			t.Fatalf("compress past the limit: %v", err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
	waitGoroutines(t, before)

	for _, after := range []time.Duration{0, time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(after, cancel)
		res, err := f.Compress(ctx, ".", []string{"d"}, -1, time.Now(), CompressOptions{Name: "c", Level: 1, Threads: 4})
		cancel()
		if err == nil {
			_ = os.Remove(filepath.Join(dir, res.Archive))
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
	waitGoroutines(t, before)
}
