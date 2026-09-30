package schedule

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/cron"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const srvID = "0199a000-0000-7000-8000-000000000001"

// servers is a fake server manager that records what runs asked for.
type servers struct {
	mu    sync.Mutex
	state server.State
	calls []string
	sent  chan string
}

func (f *servers) Status(id string) (server.Status, error) {
	if id != srvID {
		return server.Status{}, server.ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return server.Status{State: f.state}, nil
}

func (f *servers) Power(_ context.Context, _ string, a server.PowerAction, user string) error {
	f.record("power " + string(a) + " by " + user)
	return nil
}

func (f *servers) SendCommand(_, _ string, cmd string) error {
	f.mu.Lock()
	up := f.state == server.Running
	f.mu.Unlock()
	if !up {
		return errors.New("server isn't running")
	}
	f.record("command " + cmd)
	return nil
}

func (f *servers) record(c string) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	select {
	case f.sent <- c:
	default:
	}
}

func (f *servers) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// clock is a settable clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// backups is a fake backup manager: its backups are jobs that finish when
// released.
type backups struct {
	jobs    func() *jobs.Engine
	calls   atomic.Int32
	fail    atomic.Bool
	release chan struct{}
}

func (b *backups) Backup(ctx context.Context, serverID, _ string) (string, error) {
	b.calls.Add(1)
	return b.jobs().Enqueue(ctx, jobs.Spec{Type: "test.backup", ServerID: serverID})
}

func (b *backups) run(ctx context.Context, _ jobs.Job, _ io.Writer) (any, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	if b.fail.Load() {
		return nil, errors.New("disk full")
	}
	return nil, nil
}

type env struct {
	t       *testing.T
	backups *backups
	db      *store.DB
	dir     string
	clock   *clock
	servers *servers
	events  *events.Outbox
	jobs    *jobs.Engine
	s       *Scheduler
}

var t0 = time.Date(2026, 1, 1, 10, 2, 0, 0, time.UTC) // a Thursday

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Write.InsertServer(context.Background(), store.InsertServerParams{
		ID: srvID, Name: "smp", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	v := &env{t: t, db: db, dir: dir, clock: &clock{t: t0}, servers: &servers{state: server.Running, sent: make(chan string, 16)}, events: events.New(db)}
	v.backups = &backups{jobs: func() *jobs.Engine { return v.jobs }, release: make(chan struct{})}
	v.start()
	return v
}

// start starts a job engine and scheduler, as Wings does on startup.
func (v *env) start() {
	v.jobs = jobs.New(jobs.Options{Store: v.db, LogDir: filepath.Join(v.dir, "jobs"), Poll: 20 * time.Millisecond})
	v.jobs.Register("test.backup", jobs.Handler{Resumable: true, MaxAttempts: 3, Run: v.backups.run})
	v.s = New(Options{Store: v.db, Jobs: v.jobs, Events: v.events, Servers: v.servers, Backups: v.backups, Now: v.clock.now})
	if err := v.jobs.Start(context.Background()); err != nil {
		v.t.Fatal(err)
	}
	engine := v.jobs
	v.t.Cleanup(engine.Close)
}

func (v *env) create(d Definition) *Schedule {
	v.t.Helper()
	sc, err := v.s.Create(context.Background(), srvID, d)
	if err != nil {
		v.t.Fatal(err)
	}
	return sc
}

// tickAt runs the scheduler loop once at the given time.
func (v *env) tickAt(t time.Time) {
	v.clock.set(t)
	v.s.tick(context.Background())
}

// runs returns the schedule's runs (jobs), oldest first.
func (v *env) runs() []jobs.Job {
	list, err := v.jobs.List(context.Background(), srvID, 100)
	if err != nil {
		v.t.Fatal(err)
	}
	slices.Reverse(list)
	return list
}

func (v *env) waitRun(id string) jobs.Job {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j, err := v.jobs.Wait(ctx, id)
	if err != nil {
		v.t.Fatal(err)
	}
	return j
}

// eventsOf returns the events of a type, oldest first.
func (v *env) eventsOf(typ string) []events.Event {
	all, err := v.events.Since(context.Background(), 0, 1000)
	if err != nil {
		v.t.Fatal(err)
	}
	var out []events.Event
	for _, e := range all {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (v *env) get(id string) *Schedule {
	v.t.Helper()
	sc, err := v.s.Get(context.Background(), srvID, id)
	if err != nil {
		v.t.Fatal(err)
	}
	return sc
}

func cronParse(sc *Schedule) (*cron.Schedule, error) { return cron.Parse(sc.Cron, sc.Timezone) }

func cmd(c string) Step { return Step{Type: StepCommand, Command: c} }
func wait(d time.Duration) Step {
	return Step{Type: StepWait, Duration: server.Duration(d)}
}
func power(a server.PowerAction) Step { return Step{Type: StepPower, Action: a} }

func every5(steps ...Step) Definition {
	return Definition{Name: "restart", Cron: "*/5 * * * *", Enabled: true, Steps: steps}
}

func TestValidation(t *testing.T) {
	v := newEnv(t)
	ok := every5(cmd("say hi"))
	for name, mutate := range map[string]func(*Definition){
		"no name":        func(d *Definition) { d.Name = " " },
		"long name":      func(d *Definition) { d.Name = strings.Repeat("x", 101) },
		"bad cron":       func(d *Definition) { d.Cron = "every day" },
		"never runs":     func(d *Definition) { d.Cron = "0 0 31 2 *" },
		"bad timezone":   func(d *Definition) { d.Timezone = "Mars/Olympus" },
		"local timezone": func(d *Definition) { d.Timezone = "Local" },
		"no steps":       func(d *Definition) { d.Steps = nil },
		"too many steps": func(d *Definition) { d.Steps = slices.Repeat([]Step{cmd("x")}, MaxSteps+1) },
		"unknown step":   func(d *Definition) { d.Steps = []Step{{Type: "reboot"}} },
		"empty command":  func(d *Definition) { d.Steps = []Step{cmd(" ")} },
		"two lines":      func(d *Definition) { d.Steps = []Step{cmd("say a\nop me")} },
		"zero wait":      func(d *Definition) { d.Steps = []Step{wait(0)} },
		"long wait":      func(d *Definition) { d.Steps = []Step{wait(2 * time.Hour)} },
		"bad action":     func(d *Definition) { d.Steps = []Step{power("explode")} },
		"big jitter":     func(d *Definition) { d.Jitter = server.Duration(2 * time.Hour) },
		"bad missed":     func(d *Definition) { d.Missed = "replay" },
	} {
		d := ok
		d.Steps = slices.Clone(ok.Steps)
		mutate(&d)
		if _, err := v.s.Create(context.Background(), srvID, d); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := v.s.Create(context.Background(), "0199a000-0000-7000-8000-00000000dead", ok); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("unknown server: %v", err)
	}
	sc := v.create(ok)
	if sc.Timezone != "UTC" || sc.Missed != MissedSkip {
		t.Errorf("defaults: %+v", sc.Definition)
	}
	// Another server's schedule isn't reachable through this one.
	if _, err := v.s.Get(context.Background(), "0199a000-0000-7000-8000-000000000002", sc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-server get: %v", err)
	}
	for range MaxPerServer - 1 {
		v.create(ok)
	}
	if _, err := v.s.Create(context.Background(), srvID, ok); !errors.Is(err, ErrTooMany) {
		t.Errorf("over the limit: %v", err)
	}
}

func TestFiresAndRunsSteps(t *testing.T) {
	v := newEnv(t)
	sc := v.create(every5(cmd("say restarting in 1s"), wait(time.Second), power(server.PowerRestart)))
	if want := t0.Truncate(time.Hour).Add(5 * time.Minute); !sc.NextRun.Equal(want) {
		t.Fatalf("next run %v, want %v", sc.NextRun, want)
	}
	if created := v.eventsOf(EventCreated); len(created) != 1 || created[0].Data["next_run_at"] == nil {
		t.Fatalf("created events: %+v", created)
	}

	v.tickAt(t0.Add(2*time.Minute + 59*time.Second))
	if len(v.runs()) != 0 {
		t.Fatal("fired early")
	}
	v.tickAt(t0.Add(3*time.Minute + 10*time.Second))
	runs := v.runs()
	if len(runs) != 1 {
		t.Fatalf("runs after 10:05 = %d", len(runs))
	}
	j := v.waitRun(runs[0].ID)
	if j.Status != jobs.Succeeded {
		t.Fatalf("run: %+v", j)
	}
	want := []string{"command say restarting in 1s", "power restart by schedule:" + sc.ID}
	if got := v.servers.got(); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	after := v.get(sc.ID)
	if !after.NextRun.Equal(t0.Add(8*time.Minute)) || !after.LastRun.Equal(t0.Add(3*time.Minute+10*time.Second)) {
		t.Fatalf("after the run: next %v, last %v", after.NextRun, after.LastRun)
	}
	// The same tick again doesn't fire twice.
	v.tickAt(t0.Add(3*time.Minute + 20*time.Second))
	if len(v.runs()) != 1 {
		t.Fatal("fired twice")
	}
	finished := v.eventsOf(EventRunFinished)
	if len(finished) != 1 || finished[0].Data["ok"] != true || len(finished[0].Data["steps"].([]any)) != 3 {
		t.Fatalf("finished events: %+v", finished)
	}
	if q := v.eventsOf(EventRunQueued); len(q) != 1 || q[0].Data["reason"] != ReasonScheduled || q[0].Data["job_id"] != j.ID {
		t.Fatalf("queued events: %+v", q)
	}
}

func TestOnlyWhenOnline(t *testing.T) {
	v := newEnv(t)
	d := every5(cmd("save-all"))
	d.OnlyWhenOnline = true
	sc := v.create(d)
	v.servers.state = server.Offline
	v.tickAt(t0.Add(3 * time.Minute))
	if len(v.runs()) != 0 {
		t.Fatal("ran while offline")
	}
	if sk := v.eventsOf(EventRunSkipped); len(sk) != 1 || sk[0].Data["reason"] != SkipOffline {
		t.Fatalf("skipped events: %+v", sk)
	}
	// "Run now" always runs; the command then fails because it's offline.
	id, err := v.s.RunNow(context.Background(), srvID, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j := v.waitRun(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "isn't running") {
		t.Fatalf("manual run: %+v", j)
	}
	v.servers.state = server.Running
	v.tickAt(t0.Add(8 * time.Minute))
	if len(v.runs()) != 2 {
		t.Fatal("didn't run once online")
	}
}

func TestMissedRuns(t *testing.T) {
	for _, c := range []struct {
		missed string
		late   time.Duration
		runs   int
		reason string
	}{
		{MissedSkip, 4 * time.Minute, 1, ReasonScheduled}, // within the grace period: a quick restart
		{MissedSkip, 3 * time.Hour, 0, ""},
		{MissedRunOnce, 3 * time.Hour, 1, ReasonMissed},
	} {
		t.Run(c.missed+" "+c.late.String(), func(t *testing.T) {
			v := newEnv(t)
			d := every5(cmd("say hi"))
			d.Missed = c.missed
			sc := v.create(d)
			now := sc.NextRun.Add(c.late)
			v.tickAt(now)
			runs := v.runs()
			if len(runs) != c.runs {
				t.Fatalf("runs = %d, want %d (36 were missed; none are replayed)", len(runs), c.runs)
			}
			if c.runs == 0 {
				if sk := v.eventsOf(EventRunSkipped); len(sk) != 1 || sk[0].Data["reason"] != SkipMissed {
					t.Fatalf("skipped: %+v", sk)
				}
			} else if q := v.eventsOf(EventRunQueued); q[0].Data["reason"] != c.reason {
				t.Fatalf("reason: %+v", q)
			}
			if next := v.get(sc.ID).NextRun; !next.After(now) || next.Sub(now) > 5*time.Minute {
				t.Fatalf("next run %v isn't the next one after %v", next, now)
			}
		})
	}
}

func TestStillRunningAndDelete(t *testing.T) {
	v := newEnv(t)
	sc := v.create(every5(cmd("say bye"), wait(time.Hour), power(server.PowerStop)))
	v.tickAt(t0.Add(3 * time.Minute))
	if c := <-v.servers.sent; c != "command say bye" {
		t.Fatal(c)
	}
	v.tickAt(t0.Add(8 * time.Minute))
	if sk := v.eventsOf(EventRunSkipped); len(sk) != 1 || sk[0].Data["reason"] != SkipRunning {
		t.Fatalf("skipped: %+v", sk)
	}
	if _, err := v.s.RunNow(context.Background(), srvID, sc.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("run now while running: %v", err)
	}
	// Deleting the schedule cancels its run.
	if err := v.s.Delete(context.Background(), srvID, sc.ID); err != nil {
		t.Fatal(err)
	}
	if runs := v.runs(); runs[0].Status != jobs.Cancelled {
		t.Fatalf("run after delete: %+v", runs[0])
	}
	if slices.Contains(v.servers.got(), "power stop by schedule:"+sc.ID) {
		t.Fatal("the cancelled run stopped the server")
	}
	if _, err := v.s.Get(context.Background(), srvID, sc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if len(v.eventsOf(EventDeleted)) != 1 {
		t.Fatal("no deleted event")
	}
}

func TestContinueOnFailure(t *testing.T) {
	v := newEnv(t)
	v.servers.state = server.Offline
	failing := cmd("say restarting")
	d := every5(failing, power(server.PowerStart))
	stops := v.create(d)
	failing.ContinueOnFailure = true
	d.Steps = []Step{failing, power(server.PowerStart)}
	goesOn := v.create(d)

	for _, c := range []struct {
		sc     *Schedule
		status string
		calls  int
	}{{stops, jobs.Failed, 0}, {goesOn, jobs.Succeeded, 1}} {
		id, err := v.s.RunNow(context.Background(), srvID, c.sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		j := v.waitRun(id)
		n := 0
		for _, call := range v.servers.got() {
			if strings.HasSuffix(call, c.sc.ID) {
				n++
			}
		}
		if j.Status != c.status || n != c.calls {
			t.Errorf("continue_on_failure=%v: %s, %d later steps ran", c.sc.Steps[0].ContinueOnFailure, j.Status, n)
		}
	}
}

func TestDisabledAndUpdate(t *testing.T) {
	v := newEnv(t)
	d := every5(cmd("say hi"))
	d.Enabled = false
	sc := v.create(d)
	if !sc.NextRun.IsZero() {
		t.Fatal("disabled schedule has a next run")
	}
	v.tickAt(t0.Add(time.Hour))
	if len(v.runs()) != 0 {
		t.Fatal("disabled schedule ran")
	}
	d.Enabled, d.Cron, d.Timezone = true, "0 4 * * *", "America/New_York"
	up, err := v.s.Update(context.Background(), srvID, sc.ID, d)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC); !up.NextRun.Equal(want) || up.Version != 2 {
		t.Fatalf("after update: next %v (want 04:00 New York = %v), version %d", up.NextRun, want, up.Version)
	}
	// An update between a schedule becoming due and firing wins.
	stale, _ := v.db.Read.GetSchedule(context.Background(), sc.ID)
	d.Cron = "0 5 * * *"
	if _, err := v.s.Update(context.Background(), srvID, sc.ID, d); err != nil {
		t.Fatal(err)
	}
	v.clock.set(time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))
	if err := v.s.fire(context.Background(), stale, v.clock.now()); err != nil {
		t.Fatal(err)
	}
	if len(v.runs()) != 0 || !v.get(sc.ID).NextRun.Equal(time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("a stale schedule fired or overwrote the update")
	}
}

func TestJitter(t *testing.T) {
	v := newEnv(t)
	d := Definition{Name: "backup", Cron: "0 4 * * *", Enabled: true, Jitter: server.Duration(time.Hour), Steps: []Step{cmd("save-all")}}
	offsets := map[time.Duration]bool{}
	for range 10 {
		sc := v.create(d)
		off := sc.NextRun.Sub(time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC))
		if off < 0 || off >= time.Hour {
			t.Fatalf("offset %v", off)
		}
		offsets[off] = true
		// The same offset every run: daily runs stay 24h apart.
		c, _ := cronParse(sc)
		if n := sc.next(c, sc.NextRun); n.Sub(sc.NextRun) != 24*time.Hour {
			t.Fatalf("runs %v then %v", sc.NextRun, n)
		}
	}
	if len(offsets) < 8 {
		t.Fatalf("10 schedules got only %d different offsets", len(offsets))
	}
}

// A Wings restart during a run resumes it after its last finished step.
func TestResumeAfterRestart(t *testing.T) {
	v := newEnv(t)
	sc := v.create(every5(cmd("say restarting in 2s"), wait(2*time.Second), power(server.PowerRestart)))
	v.tickAt(t0.Add(3 * time.Minute))
	<-v.servers.sent
	time.Sleep(200 * time.Millisecond) // into the wait
	v.jobs.Close()

	v.clock.set(v.clock.now().Add(30 * time.Second)) // a quick restart
	v.start()
	v.s.tick(context.Background())
	j := v.waitRun(v.runs()[0].ID)
	want := []string{"command say restarting in 2s", "power restart by schedule:" + sc.ID}
	if j.Status != jobs.Succeeded || j.Attempts != 2 || !slices.Equal(v.servers.got(), want) {
		t.Fatalf("resumed run: %s (attempts %d), calls %q", j.Status, j.Attempts, v.servers.got())
	}
}

// After a long stop, an interrupted run isn't finished late.
func TestNoResumeAfterLongStop(t *testing.T) {
	v := newEnv(t)
	v.create(every5(cmd("say restarting in 2s"), wait(2*time.Second), power(server.PowerRestart)))
	v.tickAt(t0.Add(3 * time.Minute))
	<-v.servers.sent
	time.Sleep(200 * time.Millisecond)
	v.jobs.Close()

	v.clock.set(v.clock.now().Add(2 * time.Hour))
	v.start()
	j := v.waitRun(v.runs()[0].ID)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "stopped for 2h") || len(v.servers.got()) != 1 {
		t.Fatalf("run after a long stop: %+v, calls %q", j, v.servers.got())
	}
}

// A backup step waits for the backup, and fails with it.
func TestBackupStep(t *testing.T) {
	v := newEnv(t)
	v.create(every5(Step{Type: StepBackup}, cmd("say backed up")))
	v.tickAt(t0.Add(3 * time.Minute))
	time.Sleep(200 * time.Millisecond)
	if got := v.servers.got(); len(got) != 0 {
		t.Fatalf("the run went on before the backup finished: %q", got)
	}
	close(v.backups.release)
	// The run, not the backup job it started: both are created in the same
	// millisecond, so their order by ID is random.
	i := slices.IndexFunc(v.runs(), func(j jobs.Job) bool { return j.Type == JobRun })
	if j := v.waitRun(v.runs()[i].ID); j.Status != jobs.Succeeded || !slices.Equal(v.servers.got(), []string{"command say backed up"}) {
		t.Fatalf("run: %+v, calls %q", j, v.servers.got())
	}

	v.backups.fail.Store(true)
	v.tickAt(t0.Add(8 * time.Minute))
	var run jobs.Job
	for _, j := range v.runs() {
		if j.Type == JobRun && j.Status != jobs.Succeeded {
			run = v.waitRun(j.ID)
		}
	}
	if run.Status != jobs.Failed || !strings.Contains(run.Error, "disk full") || len(v.servers.got()) != 1 {
		t.Fatalf("failed backup: %+v, calls %q", run, v.servers.got())
	}
}

// A run resumed during its backup step waits for the same backup.
func TestBackupStepResume(t *testing.T) {
	v := newEnv(t)
	v.create(every5(Step{Type: StepBackup}))
	v.tickAt(t0.Add(3 * time.Minute))
	time.Sleep(200 * time.Millisecond) // the backup is running
	v.jobs.Close()

	v.clock.set(v.clock.now().Add(30 * time.Second))
	v.start()
	close(v.backups.release)
	var run jobs.Job
	for _, j := range v.runs() {
		if j.Type == JobRun {
			run = v.waitRun(j.ID)
		}
	}
	if run.Status != jobs.Succeeded || v.backups.calls.Load() != 1 {
		t.Fatalf("resumed run: %+v, %d backups", run, v.backups.calls.Load())
	}
}
