package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
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

type zipEntry struct {
	name, data string
	mode       os.FileMode // 0: a regular 0644 file
}

func makeZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, en := range entries {
		h := &zip.FileHeader{Name: en.name, Method: zip.Deflate}
		mode := en.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, en.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tree describes everything under dir: each name's type, mode, and
// contents or link target.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		desc := fi.Mode().String()
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			desc += " -> " + l
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p) //nolint:gosec // test file
			desc += fmt.Sprintf(" %d %x", len(b), crc32.ChecksumIEEE(b))
		}
		out[rel] = desc
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A hostile zip extracts the same whether its files are written one at a
// time or in parallel, and nothing lands outside the server's directory.
func TestExtractZipParallelHostile(t *testing.T) {
	// Writing through a link out fails the job; everything before it must
	// have stayed inside.
	fails := []zipEntry{
		{name: "../escape", data: "x"},
		{name: "ok", data: "ok"},
		{name: "out", data: "../outside", mode: fs.ModeSymlink | 0o777},
		{name: "out/through-link", data: "x"},
		{name: "after", data: "x"},
	}
	var entries []zipEntry
	entries = append(entries,
		zipEntry{name: "../escape", data: "x"},
		zipEntry{name: "../../outside/escape", data: "x"},
		zipEntry{name: "/abs", data: "abs"},
		zipEntry{name: `win\..\..\escape`, data: "x"},
		zipEntry{name: "out", data: "../outside", mode: fs.ModeSymlink | 0o777},
		zipEntry{name: "abs-link", data: "/etc", mode: fs.ModeSymlink | 0o777},
		zipEntry{name: "fifo", mode: fs.ModeNamedPipe | 0o644},
		zipEntry{name: "server.jar", data: "evil"},
		zipEntry{name: "suid", data: "x", mode: os.ModeSetuid | 0o755},
		// A file, then a link out at its name, then a file again: the
		// link is replaced, never written through.
		zipEntry{name: "swap", data: "first"},
		zipEntry{name: "swap", data: "../outside/swapped", mode: fs.ModeSymlink | 0o777},
		zipEntry{name: "swap", data: "last"},
		// The same name many times: the last one wins.
		zipEntry{name: "dup", data: "1"}, zipEntry{name: "dup", data: "2"}, zipEntry{name: "dup", data: "3"},
		// A directory, then a file at its name (skipped).
		zipEntry{name: "e/", mode: fs.ModeDir | 0o755}, zipEntry{name: "e/x", data: "x"}, zipEntry{name: "e", data: "file"},
		zipEntry{name: "d/", mode: fs.ModeDir | 0o700},
		zipEntry{name: "d/link", data: "../outside", mode: fs.ModeSymlink | 0o777},
	)
	for i := range 200 {
		entries = append(entries, zipEntry{name: fmt.Sprintf("many/%02d/f%d", i%7, i), data: strings.Repeat(string(rune('a'+i%26)), i*37)})
	}
	t.Run("fails", func(t *testing.T) {
		if err := compareZipExtract(t, makeZip(t, fails)); err == nil {
			t.Error("writing through a link out didn't fail")
		}
	})
	t.Run("rest", func(t *testing.T) {
		if err := compareZipExtract(t, makeZip(t, entries)); err != nil {
			t.Error(err)
		}
	})
}

// compareZipExtract extracts archive one file at a time and in parallel,
// checks both stayed inside, and that they did the same.
func compareZipExtract(t *testing.T, archive []byte) error {
	t.Helper()
	return compareExtract(t, archive, "a.zip")
}

func compareExtract(t *testing.T, archive []byte, name string) error {
	t.Helper()

	var results []ArchiveResult
	var trees []map[string]string
	var errs []error
	for _, threads := range []int{1, 8} {
		root := t.TempDir()
		dir, outside := filepath.Join(root, "server"), filepath.Join(root, "outside")
		for _, d := range []string{dir, outside} {
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), archive, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
		deny := Compile([]string{"server.jar"})
		fsys, err := Open(dir, os.Getuid(), os.Getgid(), deny)
		if err != nil {
			t.Fatal(err)
		}
		before := runtime.NumGoroutine()
		res, err := fsys.Extract(context.Background(), name, "", -1, ExtractOptions{Threads: threads})
		waitGoroutines(t, before)
		_ = fsys.Close()
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Fatalf("%d threads: wrote outside: %v", threads, entries)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 2 {
			t.Fatalf("%d threads: wrote next to the directory: %v", threads, entries)
		}
		got := tree(t, dir)
		if !strings.HasSuffix(got["server.jar"], fmt.Sprintf(" 3 %x", crc32.ChecksumIEEE([]byte("jar")))) {
			t.Errorf("%d threads: a denied file was replaced: %s", threads, got["server.jar"])
		}
		if strings.Contains(got["suid"], "u") {
			t.Errorf("%d threads: setuid kept: %s", threads, got["suid"])
		}
		for name, want := range map[string]string{"swap": "last", "dup": "3"} {
			if d, ok := got[name]; ok && !strings.HasSuffix(d, fmt.Sprintf(" %d %x", len(want), crc32.ChecksumIEEE([]byte(want)))) {
				t.Errorf("%d threads: %s is %s, want the last entry's %q", threads, name, d, want)
			}
		}
		if _, ok := got["fifo"]; ok {
			t.Errorf("%d threads: a FIFO was made", threads)
		}
		results, trees, errs = append(results, res), append(trees, got), append(errs, err)
	}
	if fmt.Sprint(errs[0]) != fmt.Sprint(errs[1]) {
		t.Errorf("errors differ: %v, %v", errs[0], errs[1])
	}
	if results[0] != results[1] {
		t.Errorf("results differ: %+v, %+v", results[0], results[1])
	}
	for name, want := range trees[0] {
		if trees[1][name] != want {
			t.Errorf("%s: %q one at a time, %q in parallel", name, want, trees[1][name])
		}
	}
	if len(trees[0]) != len(trees[1]) {
		t.Errorf("%d names one at a time, %d in parallel", len(trees[0]), len(trees[1]))
	}
	t.Logf("result %+v, err %v", results[0], errs[0])
	return errs[0]
}

// Parallel writers share one space limit: a zip bomb stops with ErrNoSpace,
// and no more than the limit is written in all.
func TestExtractZipParallelSpace(t *testing.T) {
	var entries []zipEntry
	for i := range 16 {
		entries = append(entries, zipEntry{name: fmt.Sprintf("zeros%d", i), data: strings.Repeat("\x00", 4<<20)})
	}
	archive := makeZip(t, entries)
	f, dir := testFS(t)
	if err := os.WriteFile(filepath.Join(dir, "bomb.zip"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	const limit = 9 << 20
	before := runtime.NumGoroutine()
	res, err := f.Extract(context.Background(), "bomb.zip", "out", limit, ExtractOptions{Threads: 8})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("extracting past the limit: %v", err)
	}
	waitGoroutines(t, before)
	var total int64
	for _, fi := range tree(t, filepath.Join(dir, "out")) {
		var n int64
		_, _ = fmt.Sscanf(fi[strings.IndexByte(fi, ' ')+1:], "%d", &n)
		total += n
	}
	if total > limit || res.Bytes > limit {
		t.Errorf("wrote %d bytes (result %d), limit %d", total, res.Bytes, limit)
	}
}

// The same for a hostile tar.gz, inflated ahead by pgzip, with hard links
// (which wait for every file before them) and files too big to buffer.
func TestExtractTarParallelHostile(t *testing.T) {
	big := strings.Repeat("big!", maxBuffered/4+1000)
	fails := []tarEntry{
		{name: "ok", data: "ok", typ: tar.TypeReg},
		{name: "out", link: "../outside", typ: tar.TypeSymlink},
		{name: "out/through-link", data: "x", typ: tar.TypeReg},
		{name: "after", data: "x", typ: tar.TypeReg},
	}
	entries := []tarEntry{
		{name: "../escape", data: "x", typ: tar.TypeReg},
		{name: "/abs", data: "abs", typ: tar.TypeReg},
		{name: "out", link: "../outside", typ: tar.TypeSymlink},
		{name: "hard-out", link: "../outside/secret", typ: tar.TypeLink},
		{name: "fifo", typ: tar.TypeFifo},
		{name: "dev", typ: tar.TypeChar},
		{name: "server.jar", data: "evil", typ: tar.TypeReg},
		{name: "suid", data: "x", typ: tar.TypeReg, mode: 0o4755},
		{name: "swap", data: "first", typ: tar.TypeReg},
		{name: "swap", link: "../outside/swapped", typ: tar.TypeSymlink},
		{name: "swap", data: "last", typ: tar.TypeReg},
		{name: "dup", data: "1", typ: tar.TypeReg},
		{name: "dup", data: "2", typ: tar.TypeReg},
		{name: "dup", data: "3", typ: tar.TypeReg},
		{name: "big", data: big, typ: tar.TypeReg},
		{name: "big-link", link: "big", typ: tar.TypeLink},
		{name: "denied-link", link: "server.jar", typ: tar.TypeLink},
		{name: "e/", typ: tar.TypeDir},
		{name: "e/x", data: "x", typ: tar.TypeReg},
		{name: "e", data: "file", typ: tar.TypeReg},
	}
	for i := range 300 {
		entries = append(entries, tarEntry{name: fmt.Sprintf("many/%02d/f%d", i%7, i), data: strings.Repeat(string(rune('a'+i%26)), i*41), typ: tar.TypeReg})
		if i%50 == 0 {
			entries = append(entries, tarEntry{name: fmt.Sprintf("many/l%d", i), link: fmt.Sprintf("many/%02d/f%d", i%7, i), typ: tar.TypeLink})
		}
	}
	t.Run("fails", func(t *testing.T) {
		if err := compareExtract(t, gz(t, makeTar(t, fails)), "a.tar.gz"); err == nil {
			t.Error("writing through a link out didn't fail")
		}
	})
	t.Run("rest", func(t *testing.T) {
		if err := compareExtract(t, gz(t, makeTar(t, entries)), "a.tar.gz"); err != nil {
			t.Error(err)
		}
	})
}

// The same for a tar.gz bomb, with entries written in parallel (buffered)
// and as they're read (too big to buffer).
func TestExtractTarParallelSpace(t *testing.T) {
	var entries []tarEntry
	for i := range 40 {
		size := 512 << 10
		if i%5 == 0 {
			size = 3 << 20
		}
		entries = append(entries, tarEntry{name: fmt.Sprintf("zeros%d", i), data: strings.Repeat("\x00", size), typ: tar.TypeReg})
	}
	f, dir := testFS(t)
	if err := os.WriteFile(filepath.Join(dir, "bomb.tar.gz"), gz(t, makeTar(t, entries)), 0o644); err != nil {
		t.Fatal(err)
	}
	const limit = 9 << 20
	before := runtime.NumGoroutine()
	res, err := f.Extract(context.Background(), "bomb.tar.gz", "out", limit, ExtractOptions{Threads: 8})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("extracting past the limit: %v", err)
	}
	waitGoroutines(t, before)
	var total int64
	for _, fi := range tree(t, filepath.Join(dir, "out")) {
		var n int64
		_, _ = fmt.Sscanf(fi[strings.IndexByte(fi, ' ')+1:], "%d", &n)
		total += n
	}
	if total > limit || res.Bytes > limit {
		t.Errorf("wrote %d bytes (result %d), limit %d", total, res.Bytes, limit)
	}
}

// Names an archive makes the same file (a hard link, then both names
// written again) come out as one at a time would leave them, every time.
func TestExtractParallelAliases(t *testing.T) {
	a, b := strings.Repeat("A", 300_000), strings.Repeat("B", 200_000)
	entries := []tarEntry{
		{name: "d/f", data: "first", typ: tar.TypeReg},
		{name: "d/h", link: "d/f", typ: tar.TypeLink},
		{name: "s", link: "d", typ: tar.TypeSymlink},
		{name: "d/f", data: a, typ: tar.TypeReg},
		{name: "d/h", data: b, typ: tar.TypeReg},
		{name: "s/g", data: a, typ: tar.TypeReg},
		{name: "d/g", data: b, typ: tar.TypeReg},
	}
	for i := range 30 {
		entries = append(entries, tarEntry{name: fmt.Sprintf("d/x%d", i), data: b[:i*999], typ: tar.TypeReg})
	}
	archive := gz(t, makeTar(t, entries))
	for range 10 {
		if err := compareExtract(t, archive, "a.tar.gz"); err != nil {
			t.Fatal(err)
		}
	}
}

// A writer's own failure (here, a zip entry in a method Wings can't read)
// fails the job, one at a time or in parallel, and stops every writer.
func TestExtractParallelWriterError(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range 20 {
		h := &zip.FileHeader{Name: fmt.Sprintf("f%d", i), Method: zip.Store}
		if i == 7 {
			h.Method = 99
		}
		w, err := zw.CreateRaw(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("data"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, threads := range []int{1, 8} {
		f, dir := testFS(t)
		if err := os.WriteFile(filepath.Join(dir, "a.zip"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		before := runtime.NumGoroutine()
		if _, err := f.Extract(context.Background(), "a.zip", "out", -1, ExtractOptions{Threads: threads}); !errors.Is(err, zip.ErrAlgorithm) {
			t.Errorf("%d threads: %v", threads, err)
		}
		waitGoroutines(t, before)
	}
}
