package files

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Archive benchmarks (docs/WINGS.md#benchmarks). The fixtures are
// generated once, deterministically, under $RAPTOR_BENCH_DIR (default
// <tmp>/raptor-files-bench; put it on tmpfs to measure CPU, not the disk):
//
//   - 100m: ~2k files, 100 MiB: 64 random 1 MiB files, a 32 MiB jar, and
//     small config-like text files
//   - 1g: ~22k files, 1 GiB: 900 random 1 MiB files, a 32 MiB jar, and
//     small text files
//   - many: 50k files of 256 B
//
// The archives Extract reads are written by the standard library (gzip
// level 6), not by Compress, so they stay the same as Compress changes.
//
//	go test ./internal/wings/files -run '^$' -bench 'Compress|Extract' -cpu 1,2,4 -count 10
var benchFixtures = []struct {
	name                 string
	big, bigSize, smalls int
	smallSize            int
}{
	{"100m", 64, 1 << 20, 1950, 2 << 10},
	{"1g", 900, 1 << 20, 21000, 4 << 10},
	{"many", 0, 0, 50000, 256},
}

func benchDir(b *testing.B) string {
	b.Helper()
	dir := os.Getenv("RAPTOR_BENCH_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "raptor-files-bench")
	}
	return dir
}

// benchFixture makes (or reuses) a fixture under dir/src/<name> and its
// archives under dir/archives, and returns the fixture's size in bytes.
func benchFixture(b *testing.B, dir, name string) int64 {
	b.Helper()
	src := filepath.Join(dir, "src", name)
	done := filepath.Join(dir, "src", "."+name+".done")
	if _, err := os.Stat(done); err != nil {
		if err := os.RemoveAll(src); err != nil {
			b.Fatal(err)
		}
		for _, fx := range benchFixtures {
			if fx.name == name {
				writeFixture(b, src, fx.big, fx.bigSize, fx.smalls, fx.smallSize, name != "many")
			}
		}
		for _, format := range []string{FormatTarGz, FormatZip} {
			writeReferenceArchive(b, src, filepath.Join(dir, "archives", name+"."+format), format)
		}
		for _, p := range []string{src, filepath.Join(dir, "archives")} {
			_ = filepath.WalkDir(p, func(p string, _ fs.DirEntry, _ error) error { return os.Lchown(p, benchUID(), benchUID()) })
		}
		if err := os.WriteFile(done, nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	var size int64
	_ = filepath.WalkDir(src, func(_ string, d fs.DirEntry, _ error) error {
		if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() {
			size += fi.Size()
		}
		return nil
	})
	return size
}

func writeFixture(b *testing.B, root string, big, bigSize, smalls, smallSize int, jar bool) {
	b.Helper()
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test data
	write := func(name string, data []byte) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	random := func(n int) []byte {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(rng.Uint32())
		}
		return buf
	}
	for i := range big {
		write(fmt.Sprintf("world/region/r.%d.%d.mca", i/32, i%32), random(bigSize))
	}
	if jar {
		write("server.jar", random(32<<20))
	}
	words := []string{"enabled", "true", "false", "spawn", "radius", "world", "player", "max", "tick", "view-distance", "motd", "level", "seed", "difficulty"}
	for i := range smalls {
		var sb strings.Builder
		for sb.Len() < smallSize {
			// Config-like text: mostly compressible, some noise.
			fmt.Fprintf(&sb, "%s.%s: %s\n", words[rng.IntN(len(words))], words[rng.IntN(len(words))], hex.EncodeToString(random(4)))
		}
		write(fmt.Sprintf("plugins/p%03d/c%02d.yml", i/100, i%100), []byte(sb.String()[:smallSize]))
	}
}

func writeReferenceArchive(b *testing.B, src, out, format string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		b.Fatal(err)
	}
	file, err := os.Create(out)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	bw := bufio.NewWriterSize(file, 1<<20)
	var add func(name string, fi fs.FileInfo, r io.Reader) error
	var closeAll func() error
	switch format {
	case FormatZip:
		zw := zip.NewWriter(bw)
		add = func(name string, fi fs.FileInfo, r io.Reader) error {
			h, _ := zip.FileInfoHeader(fi)
			h.Name, h.Method = name, zip.Deflate
			if fi.IsDir() {
				h.Name, h.Method = name+"/", zip.Store
			}
			w, err := zw.CreateHeader(h)
			if err == nil && r != nil {
				_, err = io.Copy(w, r)
			}
			return err
		}
		closeAll = zw.Close
	default:
		gw, _ := gzip.NewWriterLevel(bw, 6)
		tw := tar.NewWriter(gw)
		add = func(name string, fi fs.FileInfo, r io.Reader) error {
			h, _ := tar.FileInfoHeader(fi, "")
			h.Name = name
			if fi.IsDir() {
				h.Name = name + "/"
			}
			err := tw.WriteHeader(h)
			if err == nil && r != nil {
				_, err = io.Copy(tw, r)
			}
			return err
		}
		closeAll = func() error { return errors.Join(tw.Close(), gw.Close()) }
	}
	base := filepath.Base(src)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		name := path.Join(base, filepath.ToSlash(rel))
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return add(name, fi, nil)
		}
		f, err := os.Open(p) //nolint:gosec // test fixture
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return add(name, fi, f)
	})
	if err == nil {
		err = closeAll()
	}
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		b.Fatal(err)
	}
}

// benchUID owns the fixtures and what's written, as the servers' user
// does on a node (as root, files root owns would be chowned on every open).
func benchUID() int {
	if os.Getuid() == 0 {
		return 1000
	}
	return os.Getuid()
}

func benchFS(b *testing.B, dir string) *FS {
	b.Helper()
	f, err := Open(dir, benchUID(), benchUID(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = f.Close() })
	return f
}

func BenchmarkCompress(b *testing.B) {
	for _, fx := range benchFixtures {
		for _, format := range []string{FormatTarGz, FormatZip} {
			b.Run(fx.name+"/"+format, func(b *testing.B) {
				dir := benchDir(b)
				size := benchFixture(b, dir, fx.name)
				f := benchFS(b, filepath.Join(dir, "src"))
				b.SetBytes(size)
				b.ReportAllocs()
				var out int64
				for b.Loop() {
					res, err := f.Compress(context.Background(), ".", []string{fx.name}, -1, time.Now(), CompressOptions{Format: format, Name: "bench"})
					if err != nil {
						b.Fatal(err)
					}
					out = res.Bytes
					b.StopTimer()
					if err := f.root.Remove(res.Archive); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				b.ReportMetric(float64(out), "archive-B")
			})
		}
	}
}

func BenchmarkExtract(b *testing.B) {
	for _, fx := range benchFixtures {
		for _, format := range []string{FormatTarGz, FormatZip} {
			b.Run(fx.name+"/"+format, func(b *testing.B) {
				dir := benchDir(b)
				size := benchFixture(b, dir, fx.name)
				f := benchFS(b, dir)
				if err := os.RemoveAll(filepath.Join(dir, "work")); err != nil {
					b.Fatal(err)
				}
				archive := "archives/" + fx.name + "." + format
				b.SetBytes(size)
				b.ReportAllocs()
				for b.Loop() {
					res, err := f.Extract(context.Background(), archive, "work", -1, ExtractOptions{})
					if err != nil {
						b.Fatal(err)
					}
					if res.Bytes != size {
						b.Fatalf("extracted %d bytes, want %d", res.Bytes, size)
					}
					b.StopTimer()
					if err := os.RemoveAll(filepath.Join(dir, "work")); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
			})
		}
	}
}

// TestArchiveOnce runs one Compress or Extract of a fixture, for strace or
// perf: RAPTOR_ARCHIVE_ONCE=compress:many or extract:1g:zip (the fixture
// must exist: run the benchmarks once first).
func TestArchiveOnce(t *testing.T) {
	spec := os.Getenv("RAPTOR_ARCHIVE_ONCE")
	if spec == "" {
		t.Skip("RAPTOR_ARCHIVE_ONCE isn't set")
	}
	parts := append(strings.Split(spec, ":"), FormatTarGz)
	op, name, format := parts[0], parts[1], parts[2]
	dir := os.Getenv("RAPTOR_BENCH_DIR")
	f, err := Open(dir, benchUID(), benchUID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var res ArchiveResult
	switch op {
	case "compress":
		res, err = f.Compress(context.Background(), "src", []string{name}, -1, time.Now(), CompressOptions{Format: format, Name: "once"})
	case "extract":
		res, err = f.Extract(context.Background(), "archives/"+name+"."+format, "work-once", -1, ExtractOptions{})
	}
	t.Logf("%+v %v", res, err)
}
