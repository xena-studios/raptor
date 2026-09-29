package schedule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/cron"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// JobRun is the job type of a schedule run.
const JobRun = "schedule.run"

// Why a run happened, or didn't.
const (
	ReasonScheduled = "scheduled"
	ReasonMissed    = "missed" // late by more than the grace period; MissedRunOnce
	ReasonManual    = "manual" // "Run now"

	SkipMissed  = "missed"        // late by more than the grace period; MissedSkip
	SkipOffline = "offline"       // OnlyWhenOnline and the server isn't running
	SkipRunning = "still_running" // the previous run hasn't finished
)

// aliveKey holds when the scheduler last ran (unix ms), so after a restart
// Wings knows how long it was down.
const aliveKey = "scheduler.alive_at"

// Servers is what runs need from the server manager.
type Servers interface {
	Status(id string) (server.Status, error)
	Power(ctx context.Context, id string, a server.PowerAction, user string) error
	SendCommand(id, user, cmd string) error
}

// Options configure a Scheduler.
type Options struct {
	Store   *store.DB
	Jobs    *jobs.Engine
	Events  *events.Outbox
	Servers Servers
	Log     *slog.Logger
	// Grace: a run late by at most this much (a quick Wings restart) still
	// runs; later than that, the schedule's missed-run policy applies.
	// Default 5 minutes.
	Grace time.Duration
	// MaxSleep bounds how long the loop sleeps, so clock changes are noticed
	// (default 1 minute).
	MaxSleep time.Duration
	Now      func() time.Time // for tests
}

// Scheduler fires schedules.
type Scheduler struct {
	o    Options
	log  *slog.Logger
	wake chan struct{}
	// downtime is how long Wings (or the scheduler) wasn't running before
	// this start. Runs interrupted by a longer stop aren't resumed.
	downtime time.Duration
	lastBeat time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a scheduler and registers its job handler. Call it before
// Jobs.Start, and Start after.
func New(o Options) *Scheduler {
	if o.Grace == 0 {
		o.Grace = defaultGrace
	}
	if o.MaxSleep == 0 {
		o.MaxSleep = defaultMaxSleep
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{o: o, log: o.Log, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	if v, err := o.Store.Read.GetKV(ctx, aliveKey); err == nil {
		if ms, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			s.downtime = max(s.now().Sub(time.UnixMilli(ms)), 0)
		}
	}
	o.Jobs.Register(JobRun, jobs.Handler{
		Class: "schedule",
		// Not a server lock: runs mostly wait, and a later backup step will
		// queue a backup job of its own.
		Resumable:   true, // continues after its last finished step
		MaxAttempts: 3,
		Run:         s.run,
	})
	return s
}

// Start starts firing schedules.
func (s *Scheduler) Start() {
	s.wg.Go(s.loop)
}

// Close stops firing schedules. Runs in progress belong to the job engine.
func (s *Scheduler) Close() {
	s.cancel()
	s.wg.Wait()
}

func (s *Scheduler) now() time.Time {
	if s.o.Now != nil {
		return s.o.Now()
	}
	return time.Now()
}

// changed wakes the loop and the outbox after a schedule changed.
func (s *Scheduler) changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.o.Events.Wake()
}

func (s *Scheduler) loop() {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
		next := s.tick(s.ctx)
		sleep := s.o.MaxSleep
		if !next.IsZero() {
			sleep = min(sleep, next.Sub(s.now()))
		}
		timer.Reset(max(sleep, time.Second))
	}
}

// tick fires every due schedule and returns when the next one is due.
func (s *Scheduler) tick(ctx context.Context) time.Time {
	now := s.now()
	if now.Sub(s.lastBeat) >= s.o.MaxSleep/2 {
		if err := s.o.Store.Write.SetKV(ctx, store.SetKVParams{Key: aliveKey, Value: []byte(strconv.FormatInt(now.UnixMilli(), 10))}); err == nil {
			s.lastBeat = now
		}
	}
	rows, err := s.o.Store.Read.DueSchedules(ctx, nullMillis(now))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("reading due schedules failed", "err", err)
		}
		return time.Time{}
	}
	for _, r := range rows {
		if err := s.fire(ctx, r, now); err != nil && ctx.Err() == nil {
			s.log.Error("schedule failed to fire", "schedule", r.ID, "server", r.ServerID, "err", err)
		}
	}
	n, err := s.o.Store.Read.NextScheduleRun(ctx)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

// fire runs (or skips) a due schedule and moves it to its next run. Both
// happen in one transaction, so a crash can't fire it twice.
func (s *Scheduler) fire(ctx context.Context, r store.Schedule, now time.Time) error {
	sc, err := fromRow(r)
	var c *cron.Schedule
	if err == nil {
		c, err = cron.Parse(sc.Cron, sc.Timezone)
	}
	if err != nil {
		// Can't happen for a schedule Wings validated; don't retry it forever.
		_, _ = s.o.Store.Write.AdvanceSchedule(ctx, store.AdvanceScheduleParams{ID: r.ID, Version: r.Version})
		return err
	}
	due := sc.NextRun
	reason, skip := ReasonScheduled, ""
	if now.Sub(due) > s.o.Grace {
		if sc.Missed == MissedRunOnce {
			reason = ReasonMissed
		} else {
			skip = SkipMissed
		}
	}
	if skip == "" && sc.OnlyWhenOnline && !s.online(sc.ServerID) {
		skip = SkipOffline
	}
	if skip == "" {
		active, err := s.o.Store.Read.ActiveScheduleRuns(ctx, sc.ID)
		if err != nil {
			return err
		}
		if len(active) > 0 {
			skip = SkipRunning
		}
	}
	next := sc.next(c, now)
	var lastRun time.Time
	if skip == "" {
		lastRun = now
	}
	queued := false
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		n, err := q.AdvanceSchedule(ctx, store.AdvanceScheduleParams{NextRunAt: nullMillis(next), LastRunAt: nullMillis(lastRun), ID: sc.ID, Version: sc.Version})
		if err != nil || n == 0 { // changed meanwhile: its new next run stands
			return err
		}
		data := map[string]any{"schedule_id": sc.ID, "scheduled_for": due.UnixMilli()}
		if !next.IsZero() {
			data["next_run_at"] = next.UnixMilli()
		}
		if skip != "" {
			data["reason"] = skip
			_, err = events.AppendTx(ctx, q, events.Event{Type: EventRunSkipped, ServerID: sc.ServerID, Data: data})
			return err
		}
		jobID, err := s.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobRun, ServerID: sc.ServerID, Payload: runPayload{
			ScheduleID: sc.ID, Reason: reason, ScheduledFor: due.UnixMilli(), Steps: sc.Steps,
		}})
		if err != nil {
			return err
		}
		data["reason"], data["job_id"] = reason, jobID
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventRunQueued, ServerID: sc.ServerID, Data: data})
		queued = true
		return err
	})
	if err != nil {
		return err
	}
	s.o.Events.Wake()
	if queued {
		s.o.Jobs.Wake()
		s.log.Info("schedule fired", "schedule", sc.ID, "server", sc.ServerID, "name", sc.Name, "reason", reason)
	} else if skip != "" {
		s.log.Info("scheduled run skipped", "schedule", sc.ID, "server", sc.ServerID, "name", sc.Name, "reason", skip)
	}
	return nil
}

func (s *Scheduler) online(serverID string) bool {
	st, err := s.o.Servers.Status(serverID)
	return err == nil && st.State == server.Running
}

// RunNow runs a schedule right away, even if it's disabled or the server is
// offline. Its next scheduled run doesn't change. It returns the job ID.
func (s *Scheduler) RunNow(ctx context.Context, serverID, id string) (string, error) {
	sc, err := s.Get(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	var jobID string
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		active, err := q.ActiveScheduleRuns(ctx, id)
		if err != nil {
			return err
		}
		if len(active) > 0 {
			return ErrRunning
		}
		jobID, err = s.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobRun, ServerID: serverID, Payload: runPayload{
			ScheduleID: id, Reason: ReasonManual, Steps: sc.Steps,
		}})
		if err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventRunQueued, ServerID: serverID, Data: map[string]any{
			"schedule_id": id, "reason": ReasonManual, "job_id": jobID,
		}})
		return err
	})
	if err != nil {
		return "", err
	}
	s.o.Events.Wake()
	s.o.Jobs.Wake()
	return jobID, nil
}

// cancelRuns cancels a schedule's queued and running runs and waits for them.
func (s *Scheduler) cancelRuns(ctx context.Context, id string) error {
	active, err := s.o.Store.Read.ActiveScheduleRuns(ctx, id)
	if err != nil {
		return err
	}
	for _, jobID := range active {
		if err := s.o.Jobs.Cancel(ctx, jobID); err != nil {
			continue // it finished meanwhile
		}
		if _, err := s.o.Jobs.Wait(ctx, jobID); err != nil {
			return err
		}
	}
	return nil
}

// runPayload is a run's job payload. The steps are copied in when it fires,
// so editing a schedule doesn't change a run already in progress.
type runPayload struct {
	ScheduleID   string `json:"schedule_id"`
	Reason       string `json:"reason"`
	ScheduledFor int64  `json:"scheduled_for,omitempty"` // unix ms
	Steps        []Step `json:"steps"`
}

// checkpoint is a run's progress, saved after every step and when a wait
// begins.
type checkpoint struct {
	Next      int          `json:"next"`                 // the step to run next
	WaitUntil int64        `json:"wait_until,omitempty"` // unix ms, while step Next is a wait
	Results   []StepResult `json:"results"`
}

// StepResult is how a step went.
type StepResult struct {
	Type  string `json:"type"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// run is the job handler: it runs the steps in order, from the checkpoint
// if the run was interrupted by a Wings stop.
func (s *Scheduler) run(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p runPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	var cp checkpoint
	resumed, err := j.DecodeCheckpoint(&cp)
	if err != nil {
		return nil, err
	}
	if j.Attempts > 1 && s.downtime > s.o.Grace {
		// Resuming now would, say, restart a server long after its players
		// were warned. Like a missed run, it isn't made up for.
		err := fmt.Errorf("wings was stopped for %s during this run; the remaining steps were skipped", s.downtime.Round(time.Second))
		_, _ = fmt.Fprintln(log, err)
		s.finished(ctx, j, p, cp, err)
		return nil, err
	}
	if resumed {
		_, _ = fmt.Fprintf(log, "resuming after a Wings restart at step %d of %d\n", cp.Next+1, len(p.Steps))
	}
	user := "schedule:" + p.ScheduleID
	var runErr error
	for i := cp.Next; i < len(p.Steps); i++ {
		st := p.Steps[i]
		_, _ = fmt.Fprintf(log, "step %d/%d: %s\n", i+1, len(p.Steps), st)
		err := s.step(ctx, j, &cp, i, st, user)
		if ctx.Err() != nil {
			// Wings is stopping (the checkpoint has the progress) or the run
			// was cancelled.
			if errors.Is(context.Cause(ctx), jobs.ErrCancelled) {
				s.finished(ctx, j, p, cp, jobs.ErrCancelled)
			}
			return nil, context.Cause(ctx)
		}
		res := StepResult{Type: st.Type, OK: err == nil}
		if err != nil {
			res.Error = err.Error()
			_, _ = fmt.Fprintf(log, "  failed: %v\n", err)
		}
		cp.Results = append(cp.Results, res)
		cp.Next, cp.WaitUntil = i+1, 0
		if cerr := s.o.Jobs.Checkpoint(ctx, j.ID, cp); cerr != nil {
			s.log.Warn("saving schedule run progress failed", "job", j.ID, "err", cerr)
		}
		if err != nil && !st.ContinueOnFailure {
			runErr = fmt.Errorf("step %d (%s): %w", i+1, st, err)
			break
		}
	}
	s.finished(ctx, j, p, cp, runErr)
	return map[string]any{"steps": cp.Results}, runErr
}

func (s *Scheduler) step(ctx context.Context, j jobs.Job, cp *checkpoint, i int, st Step, user string) error {
	switch st.Type {
	case StepCommand:
		return s.o.Servers.SendCommand(j.ServerID, user, st.Command)
	case StepPower:
		return s.o.Servers.Power(ctx, j.ServerID, st.Action, user)
	case StepWait:
		if cp.WaitUntil == 0 {
			cp.WaitUntil = s.now().Add(time.Duration(st.Duration)).UnixMilli()
			if err := s.o.Jobs.Checkpoint(ctx, j.ID, cp); err != nil {
				s.log.Warn("saving schedule run progress failed", "job", j.ID, "err", err)
			}
		}
		t := time.NewTimer(time.UnixMilli(cp.WaitUntil).Sub(s.now()))
		defer t.Stop()
		select {
		case <-t.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("unknown step type %q (step %d)", st.Type, i+1)
}

// finished records how a run ended.
func (s *Scheduler) finished(ctx context.Context, j jobs.Job, p runPayload, cp checkpoint, runErr error) {
	data := map[string]any{"schedule_id": p.ScheduleID, "job_id": j.ID, "reason": p.Reason, "ok": runErr == nil, "steps": cp.Results}
	if runErr != nil {
		data["error"] = runErr.Error()
	}
	if _, err := s.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: EventRunFinished, ServerID: j.ServerID, Data: data}); err != nil {
		s.log.Error("recording a schedule run failed", "job", j.ID, "err", err)
	}
}
