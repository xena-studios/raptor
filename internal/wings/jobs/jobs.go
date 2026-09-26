// Package jobs is Wings' durable job engine (docs/WINGS.md#jobs): installs
// now, backups and schedules later. Jobs live in SQLite, so they survive
// Wings restarts: a job that was running when Wings stopped is resumed (if
// its handler says that's safe) or marked failed, never silently lost.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Status values.
const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// Handler runs one type of job.
type Handler struct {
	// Run does the work. Output written to log becomes the job log. A
	// Retryable error requeues the job with backoff (if attempts remain).
	Run func(ctx context.Context, j Job, log io.Writer) (result any, err error)
	// Class is the concurrency class ("install", "backup", …); at most the
	// class's limit of jobs in it run at once. "" = the default class.
	Class string
	// ServerLock: at most one such job per server runs at a time.
	ServerLock bool
	// Resumable: safe to run again from the start after Wings stopped in the
	// middle of it. Non-resumable interrupted jobs are marked failed.
	Resumable bool
	// MaxAttempts including the first (default 1). Interruptions by a Wings
	// stop count as attempts, so a job that crashes Wings can't loop forever.
	MaxAttempts int
}

// Job is a job's stored state.
type Job struct {
	ID         string
	ServerID   string
	Type       string
	Payload    json.RawMessage
	Status     string
	Attempts   int
	Error      string
	Result     json.RawMessage
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Decode unmarshals the payload.
func (j Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

// Spec describes a job to enqueue.
type Spec struct {
	Type     string
	ServerID string
	Payload  any
	RunAfter time.Time // zero = now
}

// Errors.
var (
	ErrUnknownType = errors.New("unknown job type")
	ErrNotFound    = errors.New("job not found")
	ErrShutdown    = errors.New("wings is shutting down")
	ErrCancelled   = errors.New("job cancelled")
)

type retryable struct{ err error }

func (r retryable) Error() string { return r.err.Error() }
func (r retryable) Unwrap() error { return r.err }

// Retryable marks an error as worth retrying later.
func Retryable(err error) error { return retryable{err} }

// Options configure the engine.
type Options struct {
	Store  *store.DB
	LogDir string // job logs: <LogDir>/<job-id>.log
	Log    *slog.Logger
	// Limits per class; unlisted classes (and "") use DefaultLimit.
	Limits       map[string]int
	DefaultLimit int
	// Retention of finished jobs and their logs (default 30 days).
	Retention time.Duration
	// Backoff for retryable failures: base * 2^(attempt-1), capped (defaults
	// 30s and 30m).
	BackoffBase, BackoffMax time.Duration
	// Poll is how often the queue is checked when nothing wakes it
	// (default 5s; delayed jobs and retries rely on it).
	Poll time.Duration
}

// LogLimit is how much of a job's output is kept (the end of it).
const LogLimit = 10 << 20

// Engine runs jobs.
type Engine struct {
	o        Options
	log      *slog.Logger
	handlers map[string]Handler

	mu        sync.Mutex
	running   map[string]*runningJob // by job ID
	busy      map[string]bool        // server IDs holding a server lock
	classUsed map[string]int
	wake      chan struct{}
	done      map[string][]chan struct{} // waiters per job

	ctx    context.Context
	cancel context.CancelCauseFunc
	wg     sync.WaitGroup
}

type runningJob struct {
	cancel context.CancelCauseFunc
	log    *jobLog
}

// New creates an engine. Register handlers, then Start.
func New(o Options) *Engine {
	if o.DefaultLimit < 1 {
		o.DefaultLimit = 4
	}
	if o.Retention == 0 {
		o.Retention = 30 * 24 * time.Hour
	}
	if o.BackoffBase == 0 {
		o.BackoffBase = 30 * time.Second
	}
	if o.BackoffMax == 0 {
		o.BackoffMax = 30 * time.Minute
	}
	if o.Poll == 0 {
		o.Poll = 5 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Engine{
		o: o, log: o.Log, handlers: map[string]Handler{},
		running: map[string]*runningJob{}, busy: map[string]bool{}, classUsed: map[string]int{},
		wake: make(chan struct{}, 1), done: map[string][]chan struct{}{},
		ctx: ctx, cancel: cancel,
	}
}

// Register adds a handler. Call before Start.
func (e *Engine) Register(typ string, h Handler) {
	if h.MaxAttempts < 1 {
		h.MaxAttempts = 1
	}
	e.handlers[typ] = h
}

// Enqueue adds a job and wakes the engine.
func (e *Engine) Enqueue(ctx context.Context, s Spec) (string, error) {
	var id string
	err := e.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		var err error
		id, err = e.EnqueueTx(ctx, q, s)
		return err
	})
	if err == nil {
		e.Wake()
	}
	return id, err
}

// EnqueueTx adds a job inside the caller's transaction (so, for example, a
// server and its install job are created together or not at all). Call Wake
// after the transaction commits.
func (e *Engine) EnqueueTx(ctx context.Context, q *store.Queries, s Spec) (string, error) {
	h, ok := e.handlers[s.Type]
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknownType, s.Type)
	}
	payload, err := json.Marshal(s.Payload)
	if err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	now := time.Now()
	runAfter := s.RunAfter
	if runAfter.IsZero() {
		runAfter = now
	}
	err = q.InsertJob(ctx, store.InsertJobParams{
		ID: id.String(), ServerID: s.ServerID, Type: s.Type, Payload: string(payload),
		MaxAttempts: int64(h.MaxAttempts), RunAfter: runAfter.UnixMilli(), CreatedAt: now.UnixMilli(),
	})
	return id.String(), err
}

// Wake makes the engine check the queue now.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Start recovers jobs interrupted by the last Wings stop and starts the
// dispatcher. Interrupted resumable jobs are requeued; the rest are failed.
func (e *Engine) Start(ctx context.Context) error {
	if err := os.MkdirAll(e.o.LogDir, 0o700); err != nil {
		return err
	}
	rows, err := e.o.Store.Read.RunningJobs(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, r := range rows {
		h, known := e.handlers[r.Type]
		switch {
		case known && h.Resumable && r.Attempts < r.MaxAttempts:
			e.log.Info("resuming job interrupted by a Wings stop", "job", r.ID, "type", r.Type, "server", r.ServerID)
			err = e.o.Store.Write.RequeueJob(ctx, store.RequeueJobParams{Error: "interrupted by a Wings stop; resuming", RunAfter: now, ID: r.ID})
		default:
			e.log.Warn("job interrupted by a Wings stop", "job", r.ID, "type", r.Type, "server", r.ServerID)
			err = e.o.Store.Write.FinishJob(ctx, store.FinishJobParams{Status: Failed, Error: "interrupted: Wings stopped while it was running", FinishedAt: sqlInt(now), ID: r.ID})
		}
		if err != nil {
			return err
		}
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.loop()
	}()
	e.Wake()
	return nil
}

// Close stops dispatching and interrupts running jobs, then waits for them.
// They stay "running" in SQLite, so the next Start resumes them.
func (e *Engine) Close() {
	e.cancel(ErrShutdown)
	e.wg.Wait()
}

func (e *Engine) loop() {
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	poll := time.NewTimer(0)
	defer poll.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.wake:
		case <-poll.C:
		case <-prune.C:
			e.prune()
		}
		e.dispatch()
		poll.Reset(e.o.Poll)
	}
}

// dispatch starts every runnable job that has a free slot.
func (e *Engine) dispatch() {
	rows, err := e.o.Store.Read.RunnableJobs(e.ctx, time.Now().UnixMilli())
	if err != nil {
		if e.ctx.Err() == nil {
			e.log.Error("reading the job queue failed", "err", err)
		}
		return
	}
	for _, r := range rows {
		h, ok := e.handlers[r.Type]
		if !ok {
			_ = e.o.Store.Write.FinishJob(e.ctx, store.FinishJobParams{Status: Failed, Error: "unknown job type " + r.Type, FinishedAt: sqlInt(time.Now().UnixMilli()), ID: r.ID})
			continue
		}
		e.mu.Lock()
		limit := e.o.DefaultLimit
		if l, ok := e.o.Limits[h.Class]; ok {
			limit = l
		}
		serverBusy := h.ServerLock && r.ServerID != "" && e.busy[r.ServerID]
		free := e.classUsed[h.Class] < limit && !serverBusy
		if free {
			e.classUsed[h.Class]++
			if h.ServerLock && r.ServerID != "" {
				e.busy[r.ServerID] = true
			}
		}
		e.mu.Unlock()
		if !free {
			continue
		}
		n, err := e.o.Store.Write.MarkJobRunning(e.ctx, store.MarkJobRunningParams{StartedAt: sqlInt(time.Now().UnixMilli()), ID: r.ID})
		if err != nil || n == 0 { // cancelled meanwhile, or shutting down
			e.release(h, r.ServerID)
			continue
		}
		j := fromRow(r)
		j.Attempts++
		e.start(h, j, r.MaxAttempts)
	}
}

func (e *Engine) release(h Handler, serverID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.classUsed[h.Class]--
	if h.ServerLock && serverID != "" {
		delete(e.busy, serverID)
	}
}

func (e *Engine) start(h Handler, j Job, maxAttempts int64) {
	ctx, cancel := context.WithCancelCause(e.ctx)
	log, err := newJobLog(filepath.Join(e.o.LogDir, j.ID+".log"))
	if err != nil {
		e.log.Error("job log", "job", j.ID, "err", err)
	}
	e.mu.Lock()
	e.running[j.ID] = &runningJob{cancel: cancel, log: log}
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel(nil)
		w := io.Discard
		if log != nil {
			w = log
		}
		result, runErr := safeRun(ctx, h, j, w)
		if log != nil {
			log.close()
		}
		e.finish(ctx, h, j, maxAttempts, result, runErr)
	}()
}

// safeRun turns a handler panic into a job failure instead of a Wings crash.
func safeRun(ctx context.Context, h Handler, j Job, log io.Writer) (result any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job panicked: %v", p)
		}
	}()
	return h.Run(ctx, j, log)
}

func (e *Engine) finish(ctx context.Context, h Handler, j Job, maxAttempts int64, result any, runErr error) {
	defer e.release(h, j.ServerID)
	defer func() {
		e.mu.Lock()
		delete(e.running, j.ID)
		waiters := e.done[j.ID]
		delete(e.done, j.ID)
		e.mu.Unlock()
		for _, w := range waiters {
			close(w)
		}
		e.Wake() // a slot freed up
	}()

	// Wings is stopping: leave the job "running" so the next start resumes it.
	if errors.Is(context.Cause(ctx), ErrShutdown) {
		return
	}
	db := context.WithoutCancel(e.ctx)
	now := time.Now().UnixMilli()
	if runErr == nil {
		res, _ := json.Marshal(result)
		if err := e.o.Store.Write.FinishJob(db, store.FinishJobParams{Status: Succeeded, Result: string(res), FinishedAt: sqlInt(now), ID: j.ID}); err != nil {
			e.log.Error("saving job result failed", "job", j.ID, "err", err)
		}
		return
	}
	status := Failed
	if errors.Is(context.Cause(ctx), ErrCancelled) {
		status = Cancelled
	}
	var rt retryable
	if status == Failed && errors.As(runErr, &rt) && int64(j.Attempts) < maxAttempts {
		delay := min(e.o.BackoffBase<<(j.Attempts-1), e.o.BackoffMax)
		e.log.Warn("job failed; retrying", "job", j.ID, "type", j.Type, "attempt", j.Attempts, "retry_in", delay.String(), "err", runErr)
		_ = e.o.Store.Write.RequeueJob(db, store.RequeueJobParams{Error: runErr.Error(), RunAfter: time.Now().Add(delay).UnixMilli(), ID: j.ID})
		return
	}
	if err := e.o.Store.Write.FinishJob(db, store.FinishJobParams{Status: status, Error: runErr.Error(), FinishedAt: sqlInt(now), ID: j.ID}); err != nil {
		e.log.Error("saving job failure failed", "job", j.ID, "err", err)
	}
}

// Cancel cancels a queued or running job.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	e.mu.Lock()
	r, running := e.running[id]
	e.mu.Unlock()
	if running {
		r.cancel(ErrCancelled)
		return nil
	}
	n, err := e.o.Store.Write.CancelQueuedJob(ctx, store.CancelQueuedJobParams{FinishedAt: sqlInt(time.Now().UnixMilli()), ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := e.Get(ctx, id); err != nil {
			return err
		}
		return errors.New("job already finished")
	}
	return nil
}

// Get returns a job.
func (e *Engine) Get(ctx context.Context, id string) (Job, error) {
	r, err := e.o.Store.Read.GetJob(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return fromRow(r), nil
}

// List returns the newest jobs, for one server or all ("").
func (e *Engine) List(ctx context.Context, serverID string, limit int) ([]Job, error) {
	rows, err := e.o.Store.Read.ListJobs(ctx, store.ListJobsParams{ServerID: serverID, Lim: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

// HasActive reports whether a server has a queued or running job.
func (e *Engine) HasActive(ctx context.Context, serverID string) (bool, error) {
	n, err := e.o.Store.Read.ActiveJobsForServer(ctx, serverID)
	return n > 0, err
}

// Wait blocks until the job is finished (succeeded, failed, or cancelled).
func (e *Engine) Wait(ctx context.Context, id string) (Job, error) {
	for {
		j, err := e.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		if j.Status == Succeeded || j.Status == Failed || j.Status == Cancelled {
			return j, nil
		}
		ch := make(chan struct{})
		e.mu.Lock()
		e.done[id] = append(e.done[id], ch)
		e.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(time.Second): // queued jobs, retries
		case <-ctx.Done():
			return j, ctx.Err()
		}
	}
}

// Log returns a job's output: live for a running job, from disk otherwise.
func (e *Engine) Log(id string) ([]byte, error) {
	e.mu.Lock()
	r, ok := e.running[id]
	e.mu.Unlock()
	if ok && r.log != nil {
		return r.log.bytes(), nil
	}
	b, err := os.ReadFile(filepath.Join(e.o.LogDir, id+".log")) //nolint:gosec // job IDs are UUIDs we generated
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// prune deletes finished jobs past the retention and their logs.
func (e *Engine) prune() {
	ids, err := e.o.Store.Write.PruneJobs(e.ctx, sqlInt(time.Now().Add(-e.o.Retention).UnixMilli()))
	if err != nil {
		return
	}
	for _, id := range ids {
		_ = os.Remove(filepath.Join(e.o.LogDir, id+".log"))
	}
}

func fromRow(r store.Job) Job {
	j := Job{
		ID: r.ID, ServerID: r.ServerID, Type: r.Type, Payload: json.RawMessage(r.Payload),
		Status: r.Status, Attempts: int(r.Attempts), Error: r.Error, CreatedAt: time.UnixMilli(r.CreatedAt),
	}
	if r.Result != "" {
		j.Result = json.RawMessage(r.Result)
	}
	if r.StartedAt.Valid {
		j.StartedAt = time.UnixMilli(r.StartedAt.Int64)
	}
	if r.FinishedAt.Valid {
		j.FinishedAt = time.UnixMilli(r.FinishedAt.Int64)
	}
	return j
}

func sqlInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
