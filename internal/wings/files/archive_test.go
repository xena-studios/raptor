package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/xena-studios/raptor/internal/wings/jobs"
)

type tarEntry struct {
	name, link, data string
	typ              byte
	mode             int64
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, en := range entries {
		mode := en.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: en.name, Linkname: en.link, Typeflag: en.typ, Mode: mode, Size: int64(len(en.data))}
		if en.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(en.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *env) extract(archive []byte, name string) (ArchiveResult, error) {
	e.t.Helper()
	e.write(name, string(archive))
	id, err := e.svc.Decompress(ctx, srvID, "u", name, "")
	if err != nil {
		return ArchiveResult{}, err
	}
	j := e.wait(id)
	var res ArchiveResult
	if j.Status != jobs.Succeeded {
		return res, errors.New(j.Error)
	}
	if err := json.Unmarshal(j.Result, &res); err != nil {
		e.t.Fatal(err)
	}
	return res, nil
}

func TestCompressRoundTrip(t *testing.T) {
	e := newEnv(t)
	e.servers.deny = []string{"secret.txt"}
	e.write("world/level.dat", "level")
	e.write("world/region/r.0.0.mca", strings.Repeat("r", 100_000))
	e.write("world/secret.txt", "denied")
	e.write("plugins/a.jar", "jar")
	if err := os.Symlink("level.dat", filepath.Join(e.dir, "world/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(e.dir, "world/out")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("../outside", filepath.Join(e.dir, "top-link")); err != nil {
		t.Fatal(err)
	}
	id, err := e.svc.Compress(ctx, srvID, "u", "/", []string{"world", "plugins/a.jar", "top-link"})
	if err != nil {
		t.Fatal(err)
	}
	j := e.wait(id)
	if j.Status != jobs.Succeeded {
		t.Fatalf("compress: %s %s", j.Status, j.Error)
	}
	var res ArchiveResult
	if err := json.Unmarshal(j.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.Archive != "archive-2027-01-15T080000Z.tar.gz" || res.Skipped != 1 {
		t.Fatalf("result = %+v", res)
	}
	entries, _ := os.ReadDir(e.dir)
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".part") {
			t.Errorf("left a partial archive: %s", en.Name())
		}
	}

	// Extract it elsewhere and compare.
	if err := os.Rename(filepath.Join(e.dir, res.Archive), filepath.Join(e.dir, "copy.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Mkdir(ctx, srvID, "restored"); err != nil {
		t.Fatal(err)
	}
	id, err = e.svc.Decompress(ctx, srvID, "u", "copy.tar.gz", "restored")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(id); j.Status != jobs.Succeeded {
		t.Fatalf("decompress: %s", j.Error)
	}
	if got := e.read("restored/world/region/r.0.0.mca"); len(got) != 100_000 {
		t.Errorf("region file has %d bytes", len(got))
	}
	if e.read("restored/plugins/a.jar") != "jar" || e.read("restored/world/link") != "level" {
		t.Error("files or links weren't restored")
	}
	if e.exists("restored/world/secret.txt") {
		t.Error("a denied file was archived")
	}
	if target, err := os.Readlink(filepath.Join(e.dir, "restored/world/out")); err != nil || target != "../../outside" {
		t.Errorf("link = %q, %v", target, err)
	}
	if target, err := os.Readlink(filepath.Join(e.dir, "restored/top-link")); err != nil || target != "../outside" {
		t.Errorf("named link = %q, %v", target, err)
	}
}

func TestExtractFormats(t *testing.T) {
	e := newEnv(t)
	plain := makeTar(t, []tarEntry{{name: "dir/", typ: tar.TypeDir}, {name: "dir/f", data: "tar", typ: tar.TypeReg}})
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create(`win\path.txt`)
	_, _ = w.Write([]byte("zip"))
	_ = zw.Close()
	var zst bytes.Buffer
	enc, _ := zstd.NewWriter(&zst)
	_, _ = enc.Write(plain)
	_ = enc.Close()

	for name, tc := range map[string]struct {
		data       []byte
		file, want string
	}{
		"a.tar":      {plain, "dir/f", "tar"},
		"a.tar.gz":   {gz(t, plain), "dir/f", "tar"},
		"a.tar.zst":  {zst.Bytes(), "dir/f", "tar"},
		"a.zip":      {zbuf.Bytes(), "win/path.txt", "zip"},
		"log.txt.gz": {gz(t, []byte("single")), "log.txt", "single"},
	} {
		if err := os.RemoveAll(filepath.Join(e.dir, "dir")); err != nil {
			t.Fatal(err)
		}
		if _, err := e.extract(tc.data, name); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := e.read(tc.file); got != tc.want {
			t.Errorf("%s: %s = %q", name, tc.file, got)
		}
	}
	if _, err := e.extract([]byte("not an archive at all"), "x.rar"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unknown format: %v", err)
	}
}

// Hostile archives: names that climb out, absolute names, links out
// followed by files through them, special files, setuid bits.
func TestExtractHostile(t *testing.T) {
	e := newEnv(t)
	e.servers.deny = []string{"server.jar"}
	e.write("server.jar", "jar")
	archive := makeTar(t, []tarEntry{
		{name: "../escape", data: "x", typ: tar.TypeReg},
		{name: "../../outside/escape", data: "x", typ: tar.TypeReg},
		{name: "/abs", data: "abs", typ: tar.TypeReg},
		{name: "out", link: "../outside", typ: tar.TypeSymlink},
		{name: "out/through-link", data: "x", typ: tar.TypeReg},
		{name: "abs-link", link: "/etc", typ: tar.TypeSymlink},
		{name: "abs-link/passwd", data: "x", typ: tar.TypeReg},
		{name: "hard", link: "../outside/secret", typ: tar.TypeLink},
		{name: "fifo", typ: tar.TypeFifo},
		{name: "dev", typ: tar.TypeChar},
		{name: "server.jar", data: "evil", typ: tar.TypeReg},
		{name: "suid", data: "x", typ: tar.TypeReg, mode: 0o4755},
		{name: "ok", data: "ok", typ: tar.TypeReg},
	})
	if err := os.WriteFile(filepath.Join(e.outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := e.extract(gz(t, archive), "evil.tar.gz")
	if err == nil {
		// Writing through a link out fails the job; everything before it
		// must have stayed inside.
		t.Logf("result: %+v", res)
	}
	if entries, _ := os.ReadDir(e.outside); len(entries) != 1 {
		t.Errorf("files were created outside: %v", entries)
	}
	if e.read("server.jar") != "jar" {
		t.Error("a denied file was replaced")
	}
	if e.exists("../escape") {
		t.Error("escaped")
	}
	if e.read("abs") != "abs" {
		t.Error("an absolute name wasn't put inside")
	}

	// Without the entries that fail, the rest extracts.
	archive = makeTar(t, []tarEntry{
		{name: "../escape", data: "x", typ: tar.TypeReg},
		{name: "hard", link: "../outside/secret", typ: tar.TypeLink},
		{name: "fifo", typ: tar.TypeFifo},
		{name: "server.jar", data: "evil", typ: tar.TypeReg},
		{name: "suid", data: "x", typ: tar.TypeReg, mode: 0o4755},
		{name: "ok", data: "ok", typ: tar.TypeReg},
	})
	res, err = e.extract(archive, "evil2.tar")
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Skipped != 4 {
		t.Errorf("result = %+v", res)
	}
	if fi, _ := os.Stat(filepath.Join(e.dir, "suid")); fi.Mode()&os.ModeSetuid != 0 {
		t.Error("setuid bit kept")
	}
	if e.exists("fifo") || e.exists("hard") {
		t.Error("special file or link out created")
	}
}

// With a disk limit, extraction stops when the server runs out of space.
func TestExtractSpace(t *testing.T) {
	e := newEnv(t)
	bomb := makeTar(t, []tarEntry{{name: "zeros", data: strings.Repeat("\x00", 1<<20), typ: tar.TypeReg}})
	e.servers.space = 64 << 10
	if _, err := e.extract(gz(t, bomb), "bomb.tar.gz"); err == nil || !strings.Contains(err.Error(), ErrNoSpace.Error()) {
		t.Errorf("extracting past the limit: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(e.dir, "zeros")); err == nil && fi.Size() > 64<<10 {
		t.Errorf("wrote %d bytes", fi.Size())
	}
}

// Archives are untrusted input: whatever the bytes, extracting must not
// panic, hang, write outside the directory, or pass its space limit.
func FuzzExtract(f *testing.F) {
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{0x1f, 0x8b, 0x08})
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.WriteHeader(&tar.Header{Name: "l", Linkname: "../out", Typeflag: tar.TypeSymlink})
	_ = tw.WriteHeader(&tar.Header{Name: "l/y", Typeflag: tar.TypeReg, Mode: 0o644})
	_ = tw.Close()
	f.Add(buf.Bytes())
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("a/../../b")
	_, _ = w.Write([]byte("b"))
	_ = zw.Close()
	f.Add(zbuf.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		root := t.TempDir()
		dir, outside := filepath.Join(root, "server"), filepath.Join(root, "out")
		for _, d := range []string{dir, outside} {
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "a"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		fsys, err := Open(dir, os.Getuid(), os.Getgid(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fsys.Close() }()
		res, _ := fsys.Extract(context.Background(), "a", "x", 1<<20)
		if res.Bytes > 1<<20 {
			t.Fatalf("wrote %d bytes past the limit", res.Bytes)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Fatalf("wrote outside: %v", entries)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 2 {
			t.Fatalf("wrote next to the directory: %v", entries)
		}
	})
}
