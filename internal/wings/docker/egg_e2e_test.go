//go:build e2e

// End-to-end egg tests: install and run real eggs with their unmodified images.
// They need Docker and root (for file ownership), so they run in the Wings VM
// or on a CI runner, not on a developer's Mac:
//
//	task e2e:eggs            # Paper (both formats) + Node.js in the Lima VM
//	task e2e:runtime         # networks, firewall, limits, hardening
//	go test -tags e2e -run TestEggRust ./internal/wings/docker/   # x86_64 only
//
// They set up the runtime the way Wings does (networks, raptor.slice,
// firewall table), so installs run with the real network isolation.
package docker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Container UID/GID for tests; Wings uses the raptor system user's IDs.
const testUID, testGID = 988, 988

type eggCase struct {
	egg         string
	vars        map[string]string
	memoryMiB   int64
	port        int
	doneTimeout time.Duration
	// prepare runs after the install, before the server starts (e.g. accepting
	// the EULA, which the Panel asks the user to do).
	prepare func(t *testing.T, dir string)
}

func acceptEULA(t *testing.T, dir string) {
	writeFile(t, filepath.Join(dir, "eula.txt"), "eula=true\n")
}

// Latest Minecraft on the Pelican egg, whose default image is Java 25.
func TestEggPaper(t *testing.T) {
	runEgg(t, eggCase{
		egg:         "paper.plcn_v3.yaml",
		memoryMiB:   2048,
		port:        25565,
		doneTimeout: 5 * time.Minute,
		prepare:     acceptEULA,
	})
}

// The Pterodactyl-format egg defaults to Java 21, so it's pinned to a version
// Java 21 supports (newer ones need the user to pick the Java 25 image, which
// is what the egg's "java_version" feature prompts for).
func TestEggPaperPTDL(t *testing.T) {
	runEgg(t, eggCase{
		egg:         "paper.ptdl_v2.json",
		vars:        map[string]string{"MINECRAFT_VERSION": "1.21.4"},
		memoryMiB:   2048,
		port:        25565,
		doneTimeout: 5 * time.Minute,
		prepare:     acceptEULA,
	})
}

func TestEggNode(t *testing.T) {
	runEgg(t, eggCase{
		egg:         "nodejs.ptdl_v2.json",
		vars:        map[string]string{"USER_UPLOAD": "1", "MAIN_FILE": "index.js"},
		memoryMiB:   512,
		port:        3000,
		doneTimeout: 3 * time.Minute,
		prepare: func(t *testing.T, dir string) {
			// Stand-in for a Discord bot: prints the egg's default done string and
			// stays up. A real bot needs a token, which CI doesn't have.
			writeFile(t, filepath.Join(dir, "index.js"),
				"console.log('change this text 1');\nsetInterval(() => {}, 1 << 30);\n")
		},
	})
}

func TestEggRust(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the Rust dedicated server is x86_64-only")
	}
	runEgg(t, eggCase{
		egg:         "rust.ptdl_v2.json",
		vars:        map[string]string{"RCON_PASS": "e2e", "WORLD_SIZE": "1000"},
		memoryMiB:   8192,
		port:        28015,
		doneTimeout: 30 * time.Minute,
		prepare: func(t *testing.T, dir string) {
			// Config parsers arrive in Phase 1.3; until then shrink the seeded map
			// directly so the test fits a CI runner.
			cfg := filepath.Join(dir, "server/rust/cfg/server.cfg")
			b, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			b = regexp.MustCompile(`(?m)^server\.worldsize .*$`).ReplaceAll(b, []byte("server.worldsize 1000"))
			writeFile(t, cfg, string(b))
		},
	})
}

func runEgg(t *testing.T, c eggCase) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	data, err := os.ReadFile(filepath.Join(envOr("RAPTOR_E2E_EGGS", "../../eggs/testdata"), c.egg))
	if err != nil {
		t.Fatal(err)
	}
	egg, err := eggs.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	id := randomID(t)
	base := envOr("RAPTOR_E2E_DIR", "/var/lib/raptor-e2e")
	dir := filepath.Join(base, id)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // test data directory
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	vars := egg.Defaults()
	for k, v := range c.vars {
		vars[k] = v
	}
	env := eggs.Runtime{
		ServerID:  id,
		Startup:   egg.DefaultStartup(),
		MemoryMiB: c.memoryMiB,
		IP:        "0.0.0.0",
		Port:      c.port,
		Timezone:  "UTC",
		Location:  "e2e",
		Variables: vars,
	}.Environment()

	dc := newRuntime(t)

	// Install.
	start := time.Now()
	res, err := dc.Install(ctx, containers.InstallSpec{
		ServerID:  id,
		Dir:       dir,
		TmpDir:    base,
		Install:   egg.Install,
		Env:       env,
		MemoryMiB: c.memoryMiB,
		Timeout:   2 * time.Hour,
		Output:    tailWriter(t, "install"),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Logf("install finished in %s, exit code %d", time.Since(start).Round(time.Second), res.ExitCode)

	if c.prepare != nil {
		c.prepare(t, dir)
	}
	if err := FixOwnership(dir, testUID, testGID); err != nil {
		t.Fatalf("fix ownership: %v", err)
	}

	// Run.
	id2, err := dc.Create(ctx, containers.ServerSpec{
		ServerID: id,
		Dir:      dir,
		Image:    egg.DefaultImage(),
		Env:      env,
		UID:      testUID,
		GID:      testGID,
		Limits:   containers.Limits{MemoryMiB: c.memoryMiB},
		Ports:    []containers.Port{{Port: c.port}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dc.Remove(context.WithoutCancel(ctx), id2) }()

	att, err := dc.Attach(ctx, id2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = att.Close() }()

	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(att)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		signaled := false
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			t.Logf("console: %s", line)
			if !signaled && egg.Config.IsDone(line) {
				signaled = true
				close(done)
			}
		}
	}()

	start = time.Now()
	if err := dc.Start(ctx, id2); err != nil {
		t.Fatal(err)
	}
	exited := make(chan int64, 1)
	go func() {
		code, _ := dc.Wait(ctx, id2)
		exited <- code
	}()

	select {
	case <-done:
		t.Logf("RUNNING: done string seen after %s", time.Since(start).Round(time.Second))
	case code := <-exited:
		t.Fatalf("server exited with code %d before reaching running", code)
	case <-time.After(c.doneTimeout):
		t.Fatalf("server didn't reach running within %s", c.doneTimeout)
	}

	// Stop with the egg's own stop behavior.
	start = time.Now()
	if err := dc.Stop(ctx, id2, att, egg.Config.Stop, 60*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	code := <-exited
	t.Logf("STOPPED after %s, exit code %d", time.Since(start).Round(time.Second), code)
}

func randomID(t *testing.T) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "e2e" + hex.EncodeToString(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type lineLogger struct {
	t      *testing.T
	prefix string
	buf    []byte
}

func tailWriter(t *testing.T, prefix string) *lineLogger { return &lineLogger{t: t, prefix: prefix} }

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := strings.IndexAny(string(l.buf), "\n")
		if i < 0 {
			return len(p), nil
		}
		l.t.Logf("%s: %s", l.prefix, strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
}
