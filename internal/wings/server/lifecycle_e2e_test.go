//go:build e2e

// Server lifecycle tests against a real Docker. They need root (file
// ownership, firewall), so they run in the Wings VM or on a CI runner:
//
//	task e2e:runtime
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const testUID, testGID = 988, 988

// shellEgg is a minimal egg: the "server" is a BusyBox shell. Typing
// "echo READY" makes it running, "exit N" makes it exit, and its stop
// command is "exit".
func shellEgg(install string) []byte {
	return fmt.Appendf(nil, `{
		"meta": {"version": "PTDL_v2"},
		"name": "Shell",
		"docker_images": {"BusyBox": "busybox:1"},
		"startup": "sh",
		"config": {
			"files": "{\"server.properties\": {\"parser\": \"properties\", \"find\": {\"server-port\": \"{{server.build.default.port}}\", \"greeting\": \"{{server.build.env.GREETING}}\"}}}",
			"startup": "{\"done\": \"READY\"}",
			"stop": "exit"
		},
		"scripts": {"installation": {"script": %q, "container": "busybox:1", "entrypoint": "sh"}},
		"variables": [{"name": "Greeting", "env_variable": "GREETING", "default_value": "hello", "rules": "required|string|max:20"}]
	}`, install)
}

type env struct {
	t    *testing.T
	rt   *docker.Client
	nets containers.Networks
	db   *store.DB
	dir  string
	opts Options
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	rt, err := docker.New(docker.Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	nets, err := rt.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := firewall.Rules{ServerBridge: nets.Server.Bridge, InstallBridge: nets.Install.Bridge, DNS: firewall.HostResolvers()}
	if nets.CgroupParent != "" {
		if _, err := host.ApplySlice(ctx, 0); err != nil {
			t.Fatal(err)
		}
		rules.Cgroup = nets.CgroupParent
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(envOr("RAPTOR_E2E_DIR", "/var/lib/raptor-e2e"), fmt.Sprintf("lifecycle-%d", time.Now().UnixNano()))
	for _, d := range []string{"volumes", "tmp", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil { //nolint:gosec // test directory
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, rt: rt, nets: nets, db: db, dir: dir}
	e.opts = Options{
		Runtime: rt, Store: db,
		VolumesDir: filepath.Join(dir, "volumes"), TmpDir: filepath.Join(dir, "tmp"), LogDir: filepath.Join(dir, "logs"),
		UID: testUID, GID: testGID, Timezone: "UTC", Location: "e2e",
		DockerInterface: nets.Server.Gateway.String(), ReservedPorts: []int{2022, 8443},
		StartStagger: 200 * time.Millisecond,
		OOMKills:     func() (int64, error) { return host.OOMKills(host.Slice) },
		CrashWindow:  time.Minute, CrashDelays: []time.Duration{0, time.Second},
	}
	t.Cleanup(func() {
		// Remove every server this test created, then its files.
		m := New(e.opts)
		if err := m.Reconcile(context.Background()); err == nil {
			for id := range m.List() {
				_ = m.Delete(context.Background(), id)
			}
		}
		m.Close()
		_ = db.Close()
		_ = os.RemoveAll(dir)
	})
	return e
}

func (e *env) manager() *Manager {
	m := New(e.opts)
	if err := m.Reconcile(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) create(m *Manager, port int, mutate ...func(*Config)) string {
	e.t.Helper()
	cfg := Config{
		Name:        "test",
		Egg:         shellEgg("echo installed > /mnt/server/installed.txt"),
		Limits:      containers.Limits{MemoryMiB: 128},
		Settings:    DefaultSettings(),
		Allocations: []Allocation{{IP: "0.0.0.0", Port: port, Primary: true}},
	}
	for _, f := range mutate {
		f(&cfg)
	}
	id, err := m.Create(context.Background(), cfg, CreateOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	waitState(e.t, m, id, Offline, 2*time.Minute)
	return id
}

func waitState(t *testing.T, m *Manager, id string, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st, err := m.Status(id)
		if err == nil && st.State == want {
			return
		}
		if time.Now().After(deadline) {
			var hist []string
			if err == nil {
				hist = st.Console.Tail(20)
			}
			t.Fatalf("server %s: state %q, want %q after %s (err %v)\nconsole:\n%s", id, st.State, want, timeout, err, strings.Join(hist, "\n"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ready starts the server (if needed) and makes it print its done string.
func ready(t *testing.T, m *Manager, id string) {
	t.Helper()
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Starting, 30*time.Second)
	command(t, m, id, "echo READY")
	waitState(t, m, id, Running, 30*time.Second)
}

func command(t *testing.T, m *Manager, id, cmd string) {
	t.Helper()
	for range 50 {
		err := m.SendCommand(id, "e2e", cmd)
		if err == nil {
			return
		}
		if !errors.Is(err, ErrConsoleNotReady) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("console never became ready for %q", cmd)
}

func waitConsole(t *testing.T, m *Manager, id, want string) {
	t.Helper()
	for range 100 {
		st, _ := m.Status(id)
		for _, l := range st.Console.History() {
			if strings.Contains(l, want) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	st, _ := m.Status(id)
	t.Fatalf("console never showed %q:\n%s", want, strings.Join(st.Console.Tail(30), "\n"))
}

func (e *env) startedAt(id string) time.Time {
	e.t.Helper()
	st, err := e.rt.Inspect(context.Background(), containerName(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return st.StartedAt
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestLifecycle(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	defer m.Close()
	ctx := context.Background()
	events, stop := m.Events()
	defer stop()

	port := freePort(t)
	id := e.create(m, port)
	srvDir := filepath.Join(e.opts.VolumesDir, id)
	fi, err := os.Stat(filepath.Join(srvDir, "installed.txt"))
	if err != nil {
		t.Fatalf("install script didn't run: %v", err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != testUID {
		t.Errorf("installed file owned by %d", st.Uid)
	}

	ready(t, m, id)
	props, _ := os.ReadFile(filepath.Join(srvDir, "server.properties"))
	if !strings.Contains(string(props), fmt.Sprintf("server-port=%d", port)) || !strings.Contains(string(props), "greeting=hello") {
		t.Errorf("config file not applied:\n%s", props)
	}
	command(t, m, id, "echo from-env-$GREETING")
	waitConsole(t, m, id, "from-env-hello")

	// Idempotent start.
	if err := m.Start(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Restart: a new container, back to starting.
	first := e.startedAt(id)
	if err := m.Restart(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Starting, 30*time.Second)
	if !e.startedAt(id).After(first) {
		t.Error("restart didn't start a new container")
	}
	command(t, m, id, "echo READY")
	waitState(t, m, id, Running, 30*time.Second)

	// Stop with the egg's stop command ("exit"): offline, and it stays down.
	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Offline, 10*time.Second)
	srv, _ := m.Get(ctx, id)
	if srv.DesiredState != "stopped" {
		t.Errorf("desired state %q after stop", srv.DesiredState)
	}
	if err := m.Stop(ctx, id); err != nil {
		t.Fatalf("stopping a stopped server: %v", err)
	}
	if err := m.SendCommand(id, "e2e", "echo hi"); !errors.Is(err, ErrConsoleNotReady) {
		t.Errorf("command to a stopped server: %v", err)
	}

	// Kill.
	ready(t, m, id)
	start := time.Now()
	if err := m.Kill(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Offline, 10*time.Second)
	if time.Since(start) > 10*time.Second {
		t.Error("kill was slow")
	}

	// The command was audited.
	seen := false
	for len(events) > 0 {
		ev := <-events
		if ev.Type == EventCommand && ev.Data["command"] == "echo from-env-$GREETING" {
			seen = true
		}
	}
	if !seen {
		t.Error("no audit event for the console command")
	}
}

func TestCrashPolicy(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	defer m.Close()
	events, stop := m.Events()
	defer stop()

	id := e.create(m, freePort(t))
	ready(t, m, id)

	crash := func() {
		t.Helper()
		command(t, m, id, "exit 3")
		deadline := time.After(30 * time.Second)
		for {
			select {
			case ev := <-events:
				if ev.Type == EventCrashed && ev.ServerID == id {
					if ev.Data["exit_code"] != int64(3) || len(ev.Data["console"].([]string)) == 0 {
						t.Errorf("crash event = %+v", ev.Data)
					}
					return
				}
			case <-deadline:
				t.Fatal("no crash event")
			}
		}
	}
	crash()
	waitState(t, m, id, Starting, 10*time.Second) // restarted immediately
	command(t, m, id, "echo READY")
	waitState(t, m, id, Running, 10*time.Second)

	crash()
	waitState(t, m, id, Starting, 10*time.Second) // after the 1s delay
	command(t, m, id, "echo READY")
	waitState(t, m, id, Running, 10*time.Second)

	crash() // third within the window: crash loop
	waitState(t, m, id, Crashed, 10*time.Second)
	time.Sleep(2 * time.Second)
	if st, _ := m.Status(id); st.State != Crashed {
		t.Fatalf("crash-looping server restarted anyway: %s", st.State)
	}

	// An owner can still start it.
	ready(t, m, id)

	// Clean exit counts as a crash by default, but not with CleanExitIsStop.
	srv, _ := m.Get(context.Background(), id)
	cfg := srv.Config
	cfg.Settings.CleanExitIsStop = true
	if err := m.Update(context.Background(), id, cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Restart(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	command(t, m, id, "exit 0")
	waitState(t, m, id, Offline, 10*time.Second)
	srv, _ = m.Get(context.Background(), id)
	if srv.DesiredState != "stopped" {
		t.Errorf("clean exit with CleanExitIsStop: desired %q", srv.DesiredState)
	}
}

func TestOOM(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	defer m.Close()
	events, stop := m.Events()
	defer stop()
	id := e.create(m, freePort(t), func(c *Config) { c.Settings.CrashAutoRestart = false })
	ready(t, m, id)
	// The shell is PID 1; growing its own memory past the limit gets it
	// OOM-killed, which ends the container.
	command(t, m, id, `x=$(head -c 400m /dev/zero | tr '\0' a)`)
	waitState(t, m, id, Crashed, time.Minute)
	for len(events) > 0 {
		if ev := <-events; ev.Type == EventCrashed {
			if ev.Data["reason"] != "oom" {
				t.Errorf("crash reason %v, want oom", ev.Data["reason"])
			}
			return
		}
	}
	t.Error("no crash event")
}

// Stopping Wings never stops servers; the next Wings adopts them with their
// console history (docs/SERVERS.md).
func TestReconcileAdoptsRunningServers(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	id := e.create(m, freePort(t))
	ready(t, m, id)
	command(t, m, id, "echo before-restart")
	waitConsole(t, m, id, "before-restart")
	started := e.startedAt(id)
	m.Close()

	m2 := e.manager()
	defer m2.Close()
	waitState(t, m2, id, Running, 10*time.Second)
	waitConsole(t, m2, id, "before-restart")
	command(t, m2, id, "echo after-restart")
	waitConsole(t, m2, id, "after-restart")
	if !e.startedAt(id).Equal(started) {
		t.Fatal("the container was restarted")
	}
	st, _ := m2.Status(id)
	for _, l := range st.Console.History() {
		if strings.Count(l, "before-restart") > 0 && strings.Contains(l, "echo") {
			continue
		}
	}
	// No line is duplicated by the resume.
	n := 0
	for _, l := range st.Console.History() {
		if l == "after-restart" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("\"after-restart\" appears %d times", n)
	}
}

// After a reboot (containers stopped), servers that should run are started;
// servers stopped by their owner stay stopped.
func TestReconcileStartsDesiredServers(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	up := e.create(m, freePort(t))
	down := e.create(m, freePort(t))
	ready(t, m, up)
	m.Close()
	// Simulate the reboot: the container is gone, desired_state stays.
	if err := e.rt.Stop(context.Background(), containerName(up), nil, eggsKill, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	m2 := e.manager()
	defer m2.Close()
	waitState(t, m2, up, Starting, 30*time.Second)
	if st, _ := m2.Status(down); st.State != Offline {
		t.Errorf("stopped server is %s", st.State)
	}
}

func TestShutdownAll(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	a, b := e.create(m, freePort(t)), e.create(m, freePort(t))
	ready(t, m, a)
	ready(t, m, b)
	n, err := m.ShutdownAll(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("stopped %d, err %v", n, err)
	}
	for _, id := range []string{a, b} {
		waitState(t, m, id, Offline, 10*time.Second)
		if srv, _ := m.Get(context.Background(), id); srv.DesiredState != "running" {
			t.Errorf("%s: host shutdown changed desired_state to %q", id, srv.DesiredState)
		}
	}
	m.Close()

	m2 := e.manager() // the host is back
	defer m2.Close()
	waitState(t, m2, a, Starting, 30*time.Second)
	waitState(t, m2, b, Starting, 30*time.Second)
}

func TestAllocationsAndDelete(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	defer m.Close()
	ctx := context.Background()
	port := freePort(t)
	id := e.create(m, port)

	cfg := func(p int) Config {
		return Config{
			Name: "other", Egg: shellEgg(""), Limits: containers.Limits{MemoryMiB: 128},
			Allocations: []Allocation{{IP: "0.0.0.0", Port: p, Primary: true}},
		}
	}
	if _, err := m.Create(ctx, cfg(port), CreateOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate allocation: %v", err)
	}
	if _, err := m.Create(ctx, cfg(2022), CreateOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Wings' own port: %v", err)
	}
	ln, _ := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	busy := ln.Addr().(*net.TCPAddr).Port
	if _, err := m.Create(ctx, cfg(busy), CreateOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("port used by another program: %v", err)
	}
	_ = ln.Close()

	// Allocation changes apply on the next start.
	srv, _ := m.Get(ctx, id)
	newPort := freePort(t)
	c := srv.Config
	c.Allocations = []Allocation{{IP: "0.0.0.0", Port: newPort, Primary: true}}
	if err := m.Update(ctx, id, c); err != nil {
		t.Fatal(err)
	}
	ready(t, m, id)
	props, _ := os.ReadFile(filepath.Join(e.opts.VolumesDir, id, "server.properties"))
	if !strings.Contains(string(props), fmt.Sprintf("server-port=%d", newPort)) {
		t.Errorf("new allocation not applied:\n%s", props)
	}

	if err := m.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.opts.VolumesDir, id)); !os.IsNotExist(err) {
		t.Error("files not deleted")
	}
	if _, err := e.rt.Inspect(ctx, containerName(id)); err == nil {
		t.Error("container not removed")
	}
	if _, err := m.Status(id); !errors.Is(err, ErrNotFound) {
		t.Error("server still listed")
	}
	// Its port is free for another server now.
	if _, err := m.Create(ctx, cfg(newPort), CreateOptions{}); err != nil {
		t.Errorf("port not freed: %v", err)
	}
}

func TestInstallInterruptedByWingsStop(t *testing.T) {
	e := newEnv(t)
	m := e.manager()
	id, err := m.Create(context.Background(), Config{
		Name: "slow", Egg: shellEgg("sleep 120"), Limits: containers.Limits{MemoryMiB: 128},
		Allocations: []Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Installing, 10*time.Second)
	time.Sleep(3 * time.Second)
	m.Close()

	m2 := e.manager()
	defer m2.Close()
	waitState(t, m2, id, InstallFailed, 10*time.Second)
	if _, err := e.rt.Inspect(context.Background(), containerName(id)+"-install"); err == nil {
		t.Error("install container left behind")
	}
	if err := m2.Start(context.Background(), id); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("starting a failed install: %v", err)
	}
}

var eggsKill = killStop()

// TestSeedRealDaemon creates a running server in the real Wings state
// database (Wings must be stopped), for the host-level test
// (scripts/e2e-host.sh). It's skipped unless RAPTOR_E2E_SEED_DB is set.
func TestSeedRealDaemon(t *testing.T) {
	path := os.Getenv("RAPTOR_E2E_SEED_DB")
	if path == "" {
		t.Skip("RAPTOR_E2E_SEED_DB not set")
	}
	ctx := context.Background()
	rt, err := docker.New(docker.Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	nets, err := rt.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	u, err := user.Lookup("raptor")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	m := New(Options{
		Runtime: rt, Store: db, VolumesDir: "/var/lib/raptor/volumes", TmpDir: "/var/lib/raptor/tmp", LogDir: "/var/log/raptor",
		UID: uid, GID: gid, Timezone: "UTC", DockerInterface: nets.Server.Gateway.String(),
	})
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(ctx, Config{
		Name: "host-test", Egg: shellEgg("echo installed > /mnt/server/installed.txt"),
		Limits: containers.Limits{MemoryMiB: 128}, Settings: DefaultSettings(),
		Allocations: []Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}, CreateOptions{StartAfterInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, Starting, 2*time.Minute)
	command(t, m, id, "echo READY")
	waitState(t, m, id, Running, 30*time.Second)
	m.Close() // leaves the server running, as a Wings stop does
	fmt.Println("SEEDED", id)
}

// With Docker's live-restore, restarting Docker keeps servers running; Wings
// must reconnect to their console (output and stdin) without losing or
// repeating lines.
func TestDockerRestart(t *testing.T) {
	out, err := exec.Command("docker", "info", "-f", "{{.LiveRestoreEnabled}}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Skip("Docker live-restore is off (the installer turns it on)")
	}
	e := newEnv(t)
	m := e.manager()
	defer m.Close()
	id := e.create(m, freePort(t))
	ready(t, m, id)
	command(t, m, id, "echo before-docker-restart")
	waitConsole(t, m, id, "before-docker-restart")
	started := e.startedAt(id)

	if out, err := exec.Command("systemctl", "restart", "docker").CombinedOutput(); err != nil {
		t.Fatalf("restart docker: %v: %s", err, out)
	}
	waitState(t, m, id, Running, 5*time.Second)
	command(t, m, id, "echo after-docker-restart")
	waitConsole(t, m, id, "after-docker-restart")
	if !e.startedAt(id).Equal(started) {
		t.Fatal("the container was restarted")
	}
	st, _ := m.Status(id)
	n := 0
	for _, l := range st.Console.History() {
		if l == "before-docker-restart" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("\"before-docker-restart\" appears %d times after resuming", n)
	}
}
