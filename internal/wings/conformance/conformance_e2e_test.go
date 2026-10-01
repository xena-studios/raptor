//go:build e2e

// Package conformance runs every egg in the built-in catalog through the full
// Wings lifecycle, with the real server manager, Docker, and the egg's
// unmodified images (docs/EGGS.md#conformance-test-suite):
//
//	install → prepare (EULA, uploads) → start → done → command → Wings
//	restart (the server keeps running, console intact) → stop (graceful) →
//	reinstall → start → done → stop → delete
//
// It needs root and Docker, so it runs in the Wings VM or on a CI runner:
//
//	task e2e:conformance                          # the fast tier
//	task e2e:conformance TIER=slow RUN=steam/rust
//
// Environment:
//   - RAPTOR_CONFORMANCE_TIER: fast (default), slow, manual, or all
//   - RAPTOR_CONFORMANCE_SET: certified, community, or all (default)
//   - RAPTOR_CONFORMANCE_SHARD: "i/n" runs every n-th selected egg from the
//     i-th (0-based), to split a tier across CI jobs
//   - RAPTOR_CONFORMANCE_PRUNE=1: remove unused Docker images after each egg
//     (CI runners' disks can't hold every game's image at once)
//   - RAPTOR_CONFORMANCE_REPORT: file that receives a Markdown summary
//
// -test.run picks eggs by catalog ID. Eggs for another CPU architecture are
// skipped.
package conformance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	catalog "github.com/xena-studios/raptor/eggs"
	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/e2etest"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Container UID/GID, as the raptor system user has in the VM and on CI.
const uid, gid = 988, 988

// How long a console command's reply may take.
const replyTimeout = time.Minute

func TestConformance(t *testing.T) {
	all, err := catalog.All()
	if err != nil {
		t.Fatal(err)
	}
	tier := catalog.Tier(envOr("RAPTOR_CONFORMANCE_TIER", string(catalog.TierFast)))
	set := envOr("RAPTOR_CONFORMANCE_SET", "all")
	shard, shards := 0, 1
	if v := os.Getenv("RAPTOR_CONFORMANCE_SHARD"); v != "" {
		if _, err := fmt.Sscanf(v, "%d/%d", &shard, &shards); err != nil || shards < 1 || shard < 0 || shard >= shards {
			t.Fatalf("RAPTOR_CONFORMANCE_SHARD=%q: want i/n", v)
		}
	}
	var selected []catalog.Entry
	for _, e := range all {
		if (tier == "all" || e.Test.Tier == tier) &&
			(set == "all" || (set == "certified") == e.Certified) {
			selected = append(selected, e)
		}
	}
	rep := &report{tier: tier, set: set}
	t.Cleanup(func() { rep.write(t) })
	for n, e := range selected {
		if n%shards != shard {
			continue
		}
		t.Run(e.ID, func(t *testing.T) {
			if os.Getenv("RAPTOR_CONFORMANCE_PRUNE") == "1" {
				t.Cleanup(pruneImages)
			}
			switch {
			case !e.Supports(runtime.GOARCH):
				rep.add(e, "skipped", "the game doesn't run on "+runtime.GOARCH, nil)
				t.Skipf("the game doesn't run on %s", runtime.GOARCH)
			}
			r := &run{t: t, e: e, steps: map[string]time.Duration{}}
			defer func() {
				switch {
				case t.Failed():
					rep.add(e, "FAIL", r.failure, r.steps)
				case !t.Skipped():
					rep.add(e, "pass", "", r.steps)
				}
			}()
			r.lifecycle()
		})
	}
}

// run is one egg's lifecycle.
type run struct {
	t       *testing.T
	e       catalog.Entry
	env     *env
	m       *server.Manager
	id      string
	steps   map[string]time.Duration
	failure string
}

func (r *run) fail(format string, a ...any) {
	r.t.Helper()
	r.failure = fmt.Sprintf(format, a...)
	r.t.Fatal(r.failure)
}

// step times fn and records it.
func (r *run) step(name string, fn func()) {
	r.t.Helper()
	start := time.Now()
	fn()
	r.steps[name] = time.Since(start)
	r.t.Logf("%s: %s", name, r.steps[name].Round(time.Second))
}

func (r *run) lifecycle() {
	e, t := r.e, r.t
	r.env = newEnv(t)
	r.m = r.env.manager()
	t.Cleanup(func() { r.m.Close() })
	port := freePort(t)

	r.step("install", func() {
		id, err := r.m.Create(context.Background(), server.Config{
			Name:        "conformance-" + strings.ReplaceAll(e.ID, "/", "-"),
			Egg:         e.Egg,
			EggSource:   "catalog:" + e.ID,
			Image:       e.Test.Image,
			Variables:   e.Test.Variables,
			Limits:      containers.Limits{MemoryMiB: e.Test.MemoryMiB},
			Settings:    server.DefaultSettings(),
			Allocations: []server.Allocation{{IP: "0.0.0.0", Port: port, Primary: true}},
		}, server.CreateOptions{})
		if err != nil {
			r.fail("create: %v", err)
		}
		r.id = id
		r.waitInstalled("")
	})
	r.prepare()

	r.step("start", func() { r.start() })
	r.checkConfig(port)
	if q := e.Players.Query; q != "" {
		r.step("players", func() { r.players(q, port) })
	}
	if e.Test.Command != "" {
		r.step("command", func() { r.command(e.Test.Command) })
	}
	r.step("wings_restart", func() { r.restartWings() })
	r.step("stop", func() { r.stop() })

	r.step("reinstall", func() {
		job, err := r.m.Install(context.Background(), r.id)
		if err != nil {
			r.fail("reinstall: %v", err)
		}
		r.waitInstalled(job)
	})
	r.prepare() // a reinstall keeps files; writing them again is harmless
	r.step("restart_after_reinstall", func() { r.start() })
	r.stop()

	if err := r.m.Delete(context.Background(), r.id); err != nil {
		r.fail("delete: %v", err)
	}
}

// waitInstalled waits for the server's latest install job to finish, then
// checks the install succeeded. (Waiting on the state alone isn't enough: a
// reinstall is queued while the server still shows offline.)
func (r *run) waitInstalled(job string) {
	r.t.Helper()
	if job == "" { // created servers: the job Create queued
		js, err := r.env.jobs.List(context.Background(), r.id, 1)
		if err != nil || len(js) == 0 {
			r.fail("no install job (%v)", err)
		}
		job = js[0].ID
	}
	// The job has its own timeout (the egg's); allow a margin for the image
	// pull, which happens first.
	limit := r.egg().InstallTimeout() + 30*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	j, err := r.env.jobs.Wait(ctx, job)
	if err != nil {
		r.logInstall()
		r.fail("install didn't finish within %s", limit)
	}
	st, err := r.m.Status(r.id)
	if err != nil {
		r.fail("status: %v", err)
	}
	if j.Status != jobs.Succeeded || st.State != server.Offline {
		r.logInstall()
		srv, _ := r.m.Get(context.Background(), r.id)
		r.fail("install failed (job %s, state %s): %s %s", j.Status, st.State, j.Error, srv.InstallError)
	}
}

func (r *run) egg() *eggs.Egg {
	egg, err := eggs.Parse(r.e.Egg)
	if err != nil {
		r.fail("egg: %v", err)
	}
	return egg
}

func (r *run) logInstall() {
	js, err := r.env.jobs.List(context.Background(), r.id, 1)
	if err != nil || len(js) == 0 {
		return
	}
	b, _ := r.env.jobs.Log(js[0].ID)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	r.t.Logf("install log (last %d lines):\n%s", min(len(lines), 60), strings.Join(lines[max(0, len(lines)-60):], "\n"))
}

// prepare does what the user does between install and start: accept the
// EULA, upload files.
func (r *run) prepare() {
	dir := filepath.Join(r.env.volumes, r.id)
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // server files
			r.fail("write %s: %v", name, err)
		}
		if err := os.Chown(p, uid, gid); err != nil {
			r.fail("chown %s: %v", name, err)
		}
	}
	if r.e.Test.EULA {
		write("eula.txt", "eula=true\n")
	}
	for name, content := range r.e.Test.Files {
		write(name, content)
	}
}

// start starts the server and waits for its done string, then for the
// test's ready line if it has one (eggs whose done string comes before the
// server takes commands). Only output from this start counts.
func (r *run) start() {
	r.t.Helper()
	ready := make(chan struct{})
	if r.e.Test.Ready != "" {
		st, err := r.m.Status(r.id)
		if err != nil {
			r.fail("status: %v", err)
		}
		re := regexp.MustCompile(r.e.Test.Ready)
		_, sub, unsubscribe := st.Console.Subscribe()
		defer unsubscribe()
		go func() {
			for l := range sub.C {
				if re.MatchString(stripANSI(l)) {
					close(ready)
					for range sub.C { //nolint:revive // drain until unsubscribed
					}
					return
				}
			}
		}()
	} else {
		close(ready)
	}
	if err := r.m.Start(context.Background(), r.id); err != nil {
		r.fail("start: %v", err)
	}
	timeout := time.Duration(r.e.Test.DoneTimeout)
	r.waitState(server.Running, timeout)
	select {
	case <-ready:
	case <-time.After(timeout):
		r.dumpConsole()
		r.fail("never ready: no console line matching %q", r.e.Test.Ready)
	}
}

func (r *run) stop() {
	r.t.Helper()
	if err := r.m.Stop(context.Background(), r.id); err != nil {
		r.fail("stop: %v", err)
	}
	r.waitState(server.Offline, 30*time.Second)
	for _, l := range r.console() {
		if strings.Contains(l, "; killed") {
			r.fail("the egg's stop didn't stop the server (%s)", l)
		}
	}
}

// waitState waits for a state, failing early if the server crashes.
func (r *run) waitState(want server.State, timeout time.Duration) {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := r.m.Status(r.id)
		if err != nil {
			r.fail("status: %v", err)
		}
		if st.State == want {
			return
		}
		if st.State == server.Crashed || (want == server.Running && st.State == server.Offline) {
			r.logInstall() // a start failing is often an install that didn't do its job
			r.dumpConsole()
			r.fail("server %s while waiting for %s", st.State, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.dumpConsole()
	st, _ := r.m.Status(r.id)
	r.fail("state %s after %s, want %s", st.State, timeout, want)
}

func (r *run) console() []string {
	st, err := r.m.Status(r.id)
	if err != nil {
		return nil
	}
	return st.Console.History()
}

func (r *run) dumpConsole() {
	h := r.console()
	r.t.Logf("console (last %d lines):\n%s", min(len(h), 60), strings.Join(h[max(0, len(h)-60):], "\n"))
}

// command sends a console command and waits for a reply line.
func (r *run) command(cmd string) {
	r.t.Helper()
	expect := regexp.MustCompile(r.e.Test.Expect)
	st, err := r.m.Status(r.id)
	if err != nil {
		r.fail("status: %v", err)
	}
	_, sub, unsubscribe := st.Console.Subscribe()
	defer unsubscribe()
	if err := r.m.SendCommand(r.id, "conformance", cmd); err != nil {
		r.fail("send %q: %v", cmd, err)
	}
	timeout := time.After(replyTimeout)
	for {
		select {
		case l := <-sub.C:
			if expect.MatchString(stripANSI(l)) {
				return
			}
		case <-timeout:
			r.dumpConsole()
			r.fail("no reply to %q matching %q within %s", cmd, r.e.Test.Expect, replyTimeout)
		}
	}
}

// restartWings closes the manager and starts a new one on the same state,
// as a Wings restart does: the server must keep running (same container
// start time), its console history must be refilled, and commands must work.
func (r *run) restartWings() {
	r.t.Helper()
	ctx := context.Background()
	before, err := r.env.rt.Inspect(ctx, "raptor-"+r.id)
	if err != nil {
		r.fail("inspect: %v", err)
	}
	r.m.Close()
	r.m = r.env.manager()
	r.waitState(server.Running, 30*time.Second)
	after, err := r.env.rt.Inspect(ctx, "raptor-"+r.id)
	if err != nil {
		r.fail("inspect: %v", err)
	}
	if !after.Running || !after.StartedAt.Equal(before.StartedAt) {
		r.fail("the server was restarted by a Wings restart")
	}
	if r.e.Test.Command == "" {
		return
	}
	expect := regexp.MustCompile(r.e.Test.Expect)
	deadline := time.Now().Add(10 * time.Second)
	for !slices.ContainsFunc(r.console(), func(l string) bool { return expect.MatchString(stripANSI(l)) }) {
		if time.Now().After(deadline) {
			r.dumpConsole()
			r.fail("console history lost across the Wings restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	r.command(r.e.Test.Command)
}

// checkConfig checks files the egg's config parsers must have written.
// players asks the running game for its player count the way metrics do,
// checking the catalog's players.query against the real game.
func (r *run) players(query string, port int) {
	r.t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var err error
	for range 15 { // the game may still be opening its query port
		var p metrics.Players
		if p, err = metrics.QueryPlayers(context.Background(), query, addr); err == nil {
			if p.Max <= 0 || p.Online != 0 {
				r.fail("player query (%s): %d of %d players on a new server", query, p.Online, p.Max)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	r.fail("player query (%s) on %s: %v", query, addr, err)
}

func (r *run) checkConfig(port int) {
	r.t.Helper()
	for file, want := range r.e.Test.Config {
		want = strings.ReplaceAll(want, "{{port}}", fmt.Sprint(port))
		b, err := os.ReadFile(filepath.Join(r.env.volumes, r.id, file))
		if err != nil || !strings.Contains(string(b), want) {
			r.fail("%s doesn't contain %q (%v)", file, want, err)
		}
	}
}

// env is a Wings: runtime, state database, and job engine, rooted in a
// temporary directory.
type env struct {
	t       *testing.T
	rt      *docker.Client
	db      *store.DB
	jobs    *jobs.Engine
	dir     string
	volumes string
	opts    server.Options
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

	dir := filepath.Join(envOr("RAPTOR_E2E_DIR", "/var/lib/raptor-e2e"), fmt.Sprintf("conformance-%d", time.Now().UnixNano()))
	// With RAPTOR_E2E_VOLUME (an XFS volume with project quotas), servers
	// get real disk quotas, as on a node.
	vol := &storage.Volume{Path: filepath.Join(dir, "volumes"), Soft: true}
	if v := os.Getenv("RAPTOR_E2E_VOLUME"); v != "" {
		vol = &storage.Volume{Path: v}
	}
	for _, d := range []string{vol.Path, filepath.Join(dir, "tmp"), filepath.Join(dir, "logs")} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // test directories
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, rt: rt, db: db, dir: dir, volumes: vol.Path}
	e.opts = server.Options{
		Runtime: rt, Store: db, Storage: vol,
		VolumesDir: vol.Path, TmpDir: filepath.Join(dir, "tmp"), LogDir: filepath.Join(dir, "logs"),
		MachineIDDir: filepath.Join(dir, "machine-id"),
		UID:          uid, GID: gid, Timezone: "UTC", Location: "conformance",
		DockerInterface: nets.Server.Gateway.String(), ReservedPorts: []int{2022},
		OOMKills: func() (int64, error) { return host.OOMKills(host.Slice) },
	}
	t.Cleanup(func() {
		// Whatever happened, remove the server and its files.
		m := e.manager()
		for id := range m.List() {
			_ = m.Delete(context.Background(), id)
		}
		m.Close()
		_ = db.Close()
		_ = os.RemoveAll(dir)
	})
	return e
}

// manager starts a manager with its own job engine, as the daemon does.
func (e *env) manager() *server.Manager {
	o := e.opts
	o.Events = events.New(e.db)
	e.jobs = jobs.New(jobs.Options{Store: e.db, LogDir: filepath.Join(e.dir, "logs", "jobs"), Limits: map[string]int{"install": 1}, Poll: 500 * time.Millisecond})
	o.Jobs = e.jobs
	m := server.New(o)
	if err := m.Reconcile(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	if err := e.jobs.Start(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return m
}

// pruneImages removes images no container uses, after an egg's server is
// deleted.
func pruneImages() {
	_ = exec.Command("docker", "image", "prune", "-af").Run()
}

// report collects results for the Markdown summary.
type report struct {
	mu   sync.Mutex
	tier catalog.Tier
	set  string
	rows []string
}

// Steps shown in the summary, in order.
var reportSteps = []string{"install", "start", "command", "wings_restart", "stop", "reinstall", "restart_after_reinstall"}

func (p *report) add(e catalog.Entry, result, note string, steps map[string]time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cells := []string{"`" + e.ID + "`", result}
	for _, s := range reportSteps {
		if d, ok := steps[s]; ok {
			cells = append(cells, d.Round(time.Second).String())
		} else {
			cells = append(cells, "")
		}
	}
	cells = append(cells, strings.ReplaceAll(note, "|", `\|`))
	p.rows = append(p.rows, "| "+strings.Join(cells, " | ")+" |")
}

func (p *report) write(t *testing.T) {
	path := os.Getenv("RAPTOR_CONFORMANCE_REPORT")
	if path == "" || len(p.rows) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### Egg conformance: %s tier, %s eggs, on %s\n\n", p.tier, p.set, runtime.GOARCH)
	b.WriteString("| Egg | Result | Install | Start | Command | Wings restart | Stop | Reinstall | Start again | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range p.rows {
		b.WriteString(r + "\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // report file chosen by the caller
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(b.String() + "\n")
	if err != nil {
		t.Error(err)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string { return strings.TrimRight(ansi.ReplaceAllString(s, ""), "\r") }

func freePort(t *testing.T) int { return e2etest.FreePort(t) }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
