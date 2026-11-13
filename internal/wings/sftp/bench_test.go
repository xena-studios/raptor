package sftp

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkOpenSSH drives the real Server with OpenSSH's sftp client
// (docs/WINGS.md#benchmarks), logging in with a password
// through SSH_ASKPASS. The files live under $RAPTOR_BENCH_DIR (default a
// temporary directory; put it on tmpfs to measure Wings, not the disk).
// The server runs in the test process, so -cpuprofile and -trace see it:
//
//	go test ./internal/wings/sftp -run '^$' -bench OpenSSH -cpu 1,2,4 -count 10
func BenchmarkOpenSSH(b *testing.B) {
	client, err := exec.LookPath("sftp")
	if err != nil {
		b.Skip("OpenSSH's sftp client isn't installed")
	}
	base := os.Getenv("RAPTOR_BENCH_DIR")
	if base == "" {
		base = b.TempDir()
	}
	root, err := os.MkdirTemp(base, "sftp-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, srvID)
	local := filepath.Join(root, "local")
	for _, d := range []string{dir, local} {
		if err := os.Mkdir(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	hk, err := LoadHostKey(filepath.Join(b.TempDir(), "host_key"))
	if err != nil {
		b.Fatal(err)
	}
	srv := New(Options{HostKey: hk, Auth: &panel{perms: allPerms}, Servers: &servers{dir: dir}, UID: os.Getuid(), GID: os.Getgid()})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	b.Cleanup(func() { _ = srv.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	askpass := filepath.Join(root, "askpass")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho hunter2\n"), 0o700); err != nil { //nolint:gosec // a test script
		b.Fatal(err)
	}
	big := make([]byte, 256<<20)
	_, _ = rand.Read(big)
	if err := os.WriteFile(filepath.Join(local, "big.bin"), big, 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644); err != nil {
		b.Fatal(err)
	}
	many := filepath.Join(local, "many")
	for i := range 50_000 {
		p := filepath.Join(many, fmt.Sprintf("d%03d", i/100), fmt.Sprintf("f%02d", i%100))
		if i%100 == 0 {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				b.Fatal(err)
			}
		}
		if err := os.WriteFile(p, big[i*256:i*256+256], 0o644); err != nil {
			b.Fatal(err)
		}
	}

	run := func(b *testing.B, cmds string) {
		b.Helper()
		cmd := exec.Command(client, "-q", "-P", fmt.Sprint(port), //nolint:gosec // the test's own arguments
			"-c", "aes128-gcm@openssh.com",
			"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
			"-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no",
			"alice@127.0.0.1")
		cmd.Env = append(os.Environ(), "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=:0")
		cmd.Stdin = strings.NewReader(cmds)
		cmd.Dir = local
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("sftp: %v\n%s", err, out)
		}
	}
	for _, bc := range []struct {
		name, cmds string
		size       int64
		reset      func() error
	}{
		{"put-256m", "put big.bin up.bin\n", 256 << 20, func() error { return os.Remove(filepath.Join(dir, "up.bin")) }},
		{"get-256m", "get big.bin down.bin\n", 256 << 20, func() error { return os.Remove(filepath.Join(local, "down.bin")) }},
		{"put-50k", "put -r many\n", 50_000 * 256, func() error { return os.RemoveAll(filepath.Join(dir, "many")) }},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.SetBytes(bc.size)
			for b.Loop() {
				run(b, bc.cmds)
				b.StopTimer()
				if err := bc.reset(); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}
