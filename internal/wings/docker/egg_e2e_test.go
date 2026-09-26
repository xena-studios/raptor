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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/eggs/configfile"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/install"
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
	// checkFile maps files to text the egg's config parsers must have written.
	checkFile map[string]string
}

func acceptEULA(t *testing.T, dir string) {
	writeFile(t, filepath.Join(dir, "eula.txt"), "eula=true\n")
}

// Latest Minecraft on the Pelican egg, whose default image is Java 25.
func TestEggPaper(t *testing.T) {
	runEgg(t, eggCase{
		egg:         "paper.plcn_v3.yaml",
		memoryMiB:   2048,
		port:        25570, // not Minecraft's default: only the egg's parser can set it
		doneTimeout: 5 * time.Minute,
		prepare:     acceptEULA,
		checkFile:   map[string]string{"server.properties": "server-port=25570"},
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
		// The egg's own "file" parser writes WORLD_SIZE into server.cfg.
		checkFile: map[string]string{"server/rust/cfg/server.cfg": "server.worldsize 1000"},
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

	dc := newRuntime(t)
	rt := eggs.Runtime{
		ServerID:  id,
		Startup:   egg.DefaultStartup(),
		MemoryMiB: c.memoryMiB,
		IP:        "0.0.0.0",
		Port:      c.port,
		Timezone:  "UTC",
		Location:  "e2e",
	}

	// Install: the same flow Wings runs (validation, arch check, isolated
	// install container, ownership fix).
	res, err := install.Run(ctx, dc, install.Params{
		Egg:       egg,
		Image:     egg.DefaultImage(),
		ServerID:  id,
		Dir:       dir,
		TmpDir:    base,
		Variables: c.vars,
		Env:       rt,
		UID:       testUID,
		GID:       testGID,
	})
	for _, line := range strings.Split(string(res.Log), "\n") {
		t.Logf("install: %s", strings.TrimRight(line, "\r"))
	}
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Logf("install finished in %s, exit code %d", res.Duration.Round(time.Second), res.ExitCode)

	if c.prepare != nil {
		c.prepare(t, dir)
	}

	// Config files, as before every start.
	rt.Variables = res.Variables
	env := rt.Environment()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := configfile.Apply(root, egg.Config.Files, eggs.Values{
		ServerID: id, IP: rt.IP, Port: rt.Port, MemoryMiB: rt.MemoryMiB,
		Env: res.Variables, DockerInterface: dc.nets.Server.Gateway.String(),
	}, configfile.Owner{UID: testUID, GID: testGID}); err != nil {
		t.Fatalf("config files: %v", err)
	}
	for file, want := range c.checkFile {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(b), want) {
			t.Fatalf("%s doesn't contain %q (%v):\n%s", file, want, err, b)
		}
	}
	if err := install.FixOwnership(dir, testUID, testGID); err != nil { // files prepare wrote
		t.Fatal(err)
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

	start := time.Now()
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
