package files

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopyTree(t *testing.T) {
	root := t.TempDir()
	src, dst, outside := filepath.Join(root, "ptero"), filepath.Join(root, "raptor"), filepath.Join(root, "outside")
	for _, d := range []string{src, dst, outside, filepath.Join(src, "world/region")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, data string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(src, p), []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("server.properties", "motd=hi", 0o644)
	write("world/region/r.0.0.mca", "region", 0o600)
	write("start.sh", "#!/bin/sh", 0o4755)
	_ = os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	for target, link := range map[string]string{"server.properties": "props-link", "../outside/secret": "out-link", "/etc": "etc-link"} {
		if err := os.Symlink(target, filepath.Join(src, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(src, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	in, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := Open(dst, os.Getuid(), os.Getgid(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	res, err := out.CopyTree(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Bytes != int64(len("motd=hi")+len("region")+len("#!/bin/sh")) {
		t.Errorf("result: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "world/region/r.0.0.mca")); string(b) != "region" {
		t.Errorf("nested file: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "world/region/r.0.0.mca")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode: %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dst, "start.sh")); fi.Mode()&os.ModeSetuid != 0 || fi.Mode().Perm() != 0o755 {
		t.Errorf("setuid kept: %v", fi.Mode())
	}
	for link, target := range map[string]string{"props-link": "server.properties", "out-link": "../outside/secret", "etc-link": "/etc"} {
		if got, err := os.Readlink(filepath.Join(dst, link)); err != nil || got != target {
			t.Errorf("%s: %q, %v", link, got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dst, "fifo")); err == nil {
		t.Error("FIFO copied")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("wrote outside: %v", entries)
	}
}
