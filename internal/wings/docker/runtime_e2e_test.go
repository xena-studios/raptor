//go:build e2e

package docker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/install"
)

const testImage = "busybox:1"

// newRuntime sets up the runtime the way the Wings daemon does.
func newRuntime(t *testing.T) *Client {
	t.Helper()
	ctx := context.Background()
	dc, err := New(Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.Close() })
	nets, err := dc.Setup(ctx)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	rules := firewall.Rules{
		ServerBridge:  nets.Server.Bridge,
		InstallBridge: nets.Install.Bridge,
		DNS:           firewall.HostResolvers(),
	}
	if nets.CgroupParent != "" {
		if _, err := host.ApplySlice(ctx, 0); err != nil {
			t.Fatalf("slice: %v", err)
		}
		rules.Cgroup = nets.CgroupParent
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatalf("firewall: %v", err)
	}
	return dc
}

func TestRuntimeSetup(t *testing.T) {
	dc := newRuntime(t)
	first := dc.nets
	again, err := dc.Setup(context.Background())
	if err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if again != first {
		t.Fatalf("setup isn't idempotent: %+v then %+v", first, again)
	}
	if first.Server.Subnet.Overlaps(first.Install.Subnet) {
		t.Fatalf("subnets overlap: %s, %s", first.Server.Subnet, first.Install.Subnet)
	}
	for _, n := range []containers.Network{first.Server, first.Install} {
		if _, err := net.InterfaceByName(n.Bridge); err != nil {
			t.Errorf("bridge %s: %v", n.Bridge, err)
		}
	}
	if !firewall.Present(context.Background()) {
		t.Error("firewall table not loaded")
	}
	t.Logf("networks: %+v", first)
}

// The same connectivity checks run from an install container, a server
// container, and a plain Docker container (the control).
const checkScript = `
check() {
  out=$(wget -q -T 4 -O /dev/null "$2" 2>&1)
  if [ $? -eq 0 ] || echo "$out" | grep -q "server returned error"; then
    echo "RESULT $1 reachable"
  else
    echo "RESULT $1 blocked"
  fi
}
check internet http://deb.debian.org/debian/
check host_ip http://{{HOST_IP}}:{{HOST_PORT}}/
check install_gateway http://{{INSTALL_GW}}:{{HOST_PORT}}/
check server_published http://{{HOST_IP}}:{{SERVER_PORT}}/
check server_direct http://{{SERVER_IP}}:{{SERVER_PORT}}/
check metadata http://169.254.169.254/
echo "RESULT done done"
`

// Results are printed as "RESULT <key> <value>"; "RESULT done done" ends a run.
var resultLine = regexp.MustCompile(`^RESULT (\w+) (\S+)$`)

func TestRuntimeIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dc := newRuntime(t)

	// A web server on the host, on every address.
	hostLn, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })} //nolint:gosec // test server
	go func() { _ = srv.Serve(hostLn) }()
	t.Cleanup(func() { _ = srv.Close() })

	// A server container serving HTTP on its allocation.
	serverPort := freePort(t)
	target := startServer(t, dc, containers.ServerSpec{
		Ports: []containers.Port{{Port: serverPort}},
	})
	// Allocations publish the same port inside and outside, as in Pterodactyl.
	if _, err := target.console.Send(0, "httpd -f -p "+strconv.Itoa(serverPort)+" -h /home/container &"); err != nil {
		t.Fatal(err)
	}
	waitHTTP(t, net.JoinHostPort(target.ip, strconv.Itoa(serverPort)))

	script := strings.NewReplacer(
		"{{HOST_IP}}", hostIP(t),
		"{{HOST_PORT}}", strconv.Itoa(hostLn.Addr().(*net.TCPAddr).Port),
		"{{INSTALL_GW}}", dc.nets.Install.Gateway.String(),
		"{{SERVER_PORT}}", strconv.Itoa(serverPort),
		"{{SERVER_IP}}", target.ip,
	).Replace(checkScript)

	// Install container: outbound internet only.
	var out bytes.Buffer
	dir := testDir(t)
	if _, err := dc.Install(ctx, containers.InstallSpec{
		ServerID: "e2e" + randomSuffix(t),
		Dir:      dir,
		TmpDir:   filepath.Dir(dir),
		Install:  eggs.Install{Script: script, Container: testImage, Entrypoint: "sh"},
		Timeout:  5 * time.Minute,
		Output:   &out,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	expect(t, "install", parseResults(out.String()), map[string]string{
		"internet":         "reachable",
		"host_ip":          "blocked",
		"install_gateway":  "blocked",
		"server_published": "blocked",
		"server_direct":    "blocked",
		"metadata":         "blocked",
	})

	// Server container: everything but the metadata endpoint.
	peer := startServer(t, dc, containers.ServerSpec{})
	writeFile(t, filepath.Join(peer.dir, "check.sh"), script)
	res, err := peer.console.Send(time.Minute, "sh /home/container/check.sh")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "server", res, map[string]string{
		"internet":      "reachable",
		"host_ip":       "reachable",
		"server_direct": "reachable",
		"metadata":      "blocked",
	})

	// Control: a plain Docker container on the default bridge reaches the
	// things the install container couldn't, so the blocks above are Raptor's.
	expect(t, "control", runPlain(t, dc, script), map[string]string{
		"internet":         "reachable",
		"host_ip":          "reachable",
		"install_gateway":  "reachable",
		"server_published": "reachable",
	})
}

func TestRuntimeLimits(t *testing.T) {
	dc := newRuntime(t)
	loopPort := freePort(t)
	s := startServer(t, dc, containers.ServerSpec{
		Limits: containers.Limits{MemoryMiB: 256, CPUWeight: 512, CPUPercent: 150, Cpuset: "0", PIDs: 128},
		Ports:  []containers.Port{{IP: "127.0.0.1", Port: loopPort}},
	})

	insp, err := dc.api.ContainerInspect(context.Background(), s.id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hc := insp.Container.HostConfig
	for port, binds := range hc.PortBindings {
		for _, b := range binds {
			if b.HostIP != dc.nets.Server.Gateway {
				t.Errorf("%s bound to %s, want the server network gateway %s", port, b.HostIP, dc.nets.Server.Gateway)
			}
		}
	}
	if hc.CgroupParent != dc.nets.CgroupParent {
		t.Errorf("cgroup parent %q, want %q", hc.CgroupParent, dc.nets.CgroupParent)
	}
	if dc.nets.CgroupParent != "" {
		scope := filepath.Join("/sys/fs/cgroup", dc.nets.CgroupParent, "docker-"+s.id+".scope")
		if _, err := os.Stat(scope); err != nil {
			t.Errorf("container isn't in the slice: %v", err)
		}
	}

	res, err := s.console.Send(30*time.Second, strings.Join([]string{
		`echo "RESULT uid $(id -u)"`,
		`echo "RESULT cpumax $(cat /sys/fs/cgroup/cpu.max | tr ' ' _)"`,
		`echo "RESULT weight $(cat /sys/fs/cgroup/cpu.weight)"`,
		`echo "RESULT pids $(cat /sys/fs/cgroup/pids.max)"`,
		`echo "RESULT mem $(cat /sys/fs/cgroup/memory.max)"`,
		`echo "RESULT swap $(cat /sys/fs/cgroup/memory.swap.max 2>/dev/null || echo na)"`,
		`echo "RESULT cpuset $(cat /sys/fs/cgroup/cpuset.cpus.effective)"`,
		`echo "RESULT capbnd $(grep CapBnd /proc/self/status | cut -f2)"`,
		`touch /rootfs-write 2>/dev/null; echo "RESULT rootfs $?"`,
		`echo "RESULT done done"`,
	}, "; "))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("results: %v", res)
	expect(t, "limits", res, map[string]string{
		"uid":    strconv.Itoa(testUID),
		"cpumax": "150000_100000",
		"pids":   "128",
		"cpuset": "0",
		"rootfs": "1",
	})
	if w, _ := strconv.Atoi(res["weight"]); w < 1 || w >= 100 {
		t.Errorf("cpu.weight %q: want below the default of 100 for half the default shares", res["weight"])
	}
	if mem, _ := strconv.ParseInt(res["mem"], 10, 64); abs(mem-boundedMemory(256)) > 64<<10 {
		t.Errorf("memory.max %s, want about %d", res["mem"], boundedMemory(256))
	}
	if sw := res["swap"]; sw != "0" && sw != "na" {
		t.Errorf("memory.swap.max %s, want 0", sw)
	}
	if bnd, err := strconv.ParseUint(res["capbnd"], 16, 64); err != nil || bnd&(1<<13) != 0 {
		t.Errorf("CapBnd %q still has NET_RAW", res["capbnd"])
	}
}

func TestRuntimePortInUse(t *testing.T) {
	dc := newRuntime(t)
	ln, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, err = dc.Create(context.Background(), containers.ServerSpec{
		ServerID: "e2e" + randomSuffix(t),
		Dir:      testDir(t),
		Image:    testImage,
		UID:      testUID,
		GID:      testGID,
		Ports:    []containers.Port{{Port: ln.Addr().(*net.TCPAddr).Port}},
	})
	if !errors.Is(err, containers.ErrPortInUse) {
		t.Fatalf("got %v, want ErrPortInUse", err)
	}
}

// Wings must never touch containers it doesn't manage, even when the name
// collides with one of its own.
func TestRuntimeForeignContainer(t *testing.T) {
	ctx := context.Background()
	dc := newRuntime(t)
	if err := dc.EnsureImage(ctx, testImage, nil); err != nil {
		t.Fatal(err)
	}
	id := "e2e" + randomSuffix(t)
	foreign, err := dc.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   "raptor-" + id,
		Config: &container.Config{Image: testImage},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.remove(context.Background(), foreign.ID) })

	_, err = dc.Create(ctx, containers.ServerSpec{ServerID: id, Dir: testDir(t), Image: testImage, UID: testUID, GID: testGID})
	if err == nil || !strings.Contains(err.Error(), "isn't managed by Wings") {
		t.Fatalf("create over a foreign container: got %v", err)
	}
	if err := dc.Remove(ctx, foreign.ID); err == nil {
		t.Fatal("Remove deleted a foreign container")
	}
	list, err := dc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == foreign.ID {
			t.Fatal("List returned a foreign container")
		}
	}
	if _, err := dc.api.ContainerInspect(ctx, foreign.ID, client.ContainerInspectOptions{}); err != nil {
		t.Fatalf("foreign container is gone: %v", err)
	}
}

func TestRuntimeHostNetwork(t *testing.T) {
	ctx := context.Background()
	dc := newRuntime(t)
	id, err := dc.Create(ctx, containers.ServerSpec{
		ServerID: "e2e" + randomSuffix(t), Dir: testDir(t), Image: testImage, UID: testUID, GID: testGID,
		HostNetwork: true,
		Ports:       []containers.Port{{Port: freePort(t)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.Remove(context.Background(), id) })
	insp, err := dc.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hc := insp.Container.HostConfig; hc.NetworkMode != "host" || len(hc.PortBindings) != 0 {
		t.Fatalf("network mode %q, bindings %v", hc.NetworkMode, hc.PortBindings)
	}
}

func TestRuntimeCheckArch(t *testing.T) {
	dc := newRuntime(t)
	ctx := context.Background()
	if err := dc.CheckArch(ctx, testImage); err != nil {
		t.Fatalf("multi-arch image: %v", err)
	}
	// Docker's official per-architecture repositories hold single-arch images.
	other := "arm64v8/busybox:latest"
	if goruntime.GOARCH == "arm64" {
		other = "amd64/busybox:latest"
	}
	if err := dc.CheckArch(ctx, other); !errors.Is(err, containers.ErrUnsupportedArch) {
		t.Fatalf("%s: got %v, want ErrUnsupportedArch", other, err)
	}
}

// The install flow around the container: validation happens before anything
// runs, a failing script is a warning, and the egg's timeout is enforced.
func TestRuntimeInstallFlow(t *testing.T) {
	ctx := context.Background()
	dc := newRuntime(t)
	egg := func(script, timeout string) *eggs.Egg {
		e := &eggs.Egg{
			Install:   eggs.Install{Script: script, Container: testImage, Entrypoint: "sh"},
			Variables: []eggs.Variable{{Env: "VERSION", Default: "1.0", Rules: []string{"required", "string", "max:10"}}},
		}
		e.Raptor.Install.Timeout = timeout
		return e
	}
	run := func(e *eggs.Egg, vars map[string]string) (install.Result, error) {
		dir := testDir(t)
		return install.Run(ctx, dc, install.Params{
			Egg: e, Image: testImage, ServerID: "e2e" + randomSuffix(t), Dir: dir, TmpDir: filepath.Dir(dir),
			Variables: vars, Env: eggs.Runtime{MemoryMiB: 256}, UID: testUID, GID: testGID,
		})
	}

	_, err := run(egg("echo never", ""), map[string]string{"VERSION": "this-is-far-too-long"})
	var ve eggs.VariableErrors
	if !errors.As(err, &ve) {
		t.Fatalf("invalid variable: got %v, want VariableErrors", err)
	}

	res, err := run(egg(`echo "version $VERSION"; exit 3`, ""), nil)
	if err != nil || res.ExitCode != 3 || !strings.Contains(string(res.Log), "version 1.0") {
		t.Fatalf("failing script: err=%v exit=%d log=%q", err, res.ExitCode, res.Log)
	}

	start := time.Now()
	_, err = run(egg("sleep 120", "3s"), nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > time.Minute {
		t.Fatalf("timeout: got %v after %s", err, time.Since(start))
	}
}

// --- helpers ---

type runningServer struct {
	id, ip, dir string
	console     *lineConsole
}

// startServer runs a BusyBox server container whose console is a shell.
func startServer(t *testing.T, dc *Client, spec containers.ServerSpec) runningServer {
	t.Helper()
	ctx := context.Background()
	spec.ServerID = "e2e" + randomSuffix(t)
	spec.Dir = testDir(t)
	spec.Image = testImage
	spec.UID, spec.GID = testUID, testGID
	if spec.Limits.MemoryMiB == 0 {
		spec.Limits.MemoryMiB = 128
	}
	if err := install.FixOwnership(spec.Dir, testUID, testGID); err != nil {
		t.Fatal(err)
	}
	id, err := dc.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.Remove(context.Background(), id) })
	att, err := dc.Attach(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = att.Close() })
	lc := newLineConsole(t, att)
	if err := dc.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	insp, err := dc.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ip string
	if ep := insp.Container.NetworkSettings.Networks[dc.nets.Server.Name]; ep != nil {
		ip = ep.IPAddress.String()
	}
	return runningServer{id: id, ip: ip, dir: spec.Dir, console: lc}
}

// lineConsole collects RESULT lines from a shell console.
type lineConsole struct {
	c       containers.Console
	mu      sync.Mutex
	results map[string]string
	done    chan struct{}
}

func newLineConsole(t *testing.T, c containers.Console) *lineConsole {
	lc := &lineConsole{c: c, results: map[string]string{}, done: make(chan struct{}, 1)}
	go func() {
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			t.Logf("console: %s", line)
			if m := resultLine.FindStringSubmatch(line); m != nil {
				lc.mu.Lock()
				if m[1] == "done" {
					select {
					case lc.done <- struct{}{}:
					default:
					}
				} else {
					lc.results[m[1]] = m[2]
				}
				lc.mu.Unlock()
			}
		}
	}()
	return lc
}

// Send runs a command. With a timeout, it waits for "RESULT done done" and
// returns the results collected so far.
func (lc *lineConsole) Send(timeout time.Duration, cmd string) (map[string]string, error) {
	if err := lc.c.Send(cmd); err != nil || timeout == 0 {
		return nil, err
	}
	select {
	case <-lc.done:
	case <-time.After(timeout):
		return nil, fmt.Errorf("no result within %s", timeout)
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	out := make(map[string]string, len(lc.results))
	for k, v := range lc.results {
		out[k] = v
	}
	return out, nil
}

func parseResults(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if m := resultLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil && m[1] != "done" {
			out[m[1]] = m[2]
		}
	}
	return out
}

func expect(t *testing.T, who string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, want %q", who, k, got[k], v)
		}
	}
}

// runPlain runs the script in an unmanaged container on Docker's default bridge.
func runPlain(t *testing.T, dc *Client, script string) map[string]string {
	t.Helper()
	ctx := context.Background()
	created, err := dc.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: testImage, Cmd: []string{"sh", "-c", script}},
		HostConfig: &container.HostConfig{NetworkMode: "bridge"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dc.remove(context.Background(), created.ID) }()
	if err := dc.Start(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dc.Wait(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	logs, err := dc.api.ContainerLogs(ctx, created.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	// Non-TTY logs are multiplexed with 8-byte frame headers.
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(logs)
	for b := buf.Bytes(); len(b) >= 8; {
		n := int(b[4])<<24 | int(b[5])<<16 | int(b[6])<<8 | int(b[7])
		if len(b) < 8+n {
			break
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	t.Logf("control output:\n%s", out.String())
	return parseResults(out.String())
}

func waitHTTP(t *testing.T, addr string) {
	t.Helper()
	for range 50 {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s never came up", addr)
}

// hostIP returns the host's primary address (the one its default route uses).
func hostIP(t *testing.T) string {
	t.Helper()
	c, err := net.Dial("udp", "1.1.1.1:53") //nolint:noctx // no packets are sent
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	for range 20 {
		l, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if containers.CheckPorts([]containers.Port{{Port: p}}) == nil {
			return p
		}
	}
	t.Fatal("no free port")
	return 0
}

func testDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(envOr("RAPTOR_E2E_DIR", "/var/lib/raptor-e2e"), "e2e"+randomSuffix(t))
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // test data directory
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func randomSuffix(t *testing.T) string { return strings.TrimPrefix(randomID(t), "e2e") }

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
