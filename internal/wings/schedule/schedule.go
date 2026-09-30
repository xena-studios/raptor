// Package schedule runs each server's schedules (docs/WINGS.md#scheduler):
// cron expressions in the schedule's own time zone, firing multi-step runs
// (console commands, waits, power actions) as durable jobs. Wings runs them
// on its own, whether or not the Panel is reachable.
package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/cron"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Step types.
const (
	StepCommand = "command" // send a console command
	StepWait    = "wait"    // wait before the next step
	StepPower   = "power"   // start, stop, restart, or kill the server
	StepBackup  = "backup"  // back up the server and wait for it to finish
)

// Missed-run policies: what happens to a run whose time passed while Wings
// wasn't running (or more than the grace period ago).
const (
	MissedSkip    = "skip"     // skip it; the next run is the next one on the clock
	MissedRunOnce = "run_once" // run it once as soon as Wings is back
)

// Limits.
const (
	MaxSteps        = 20
	MaxWait         = time.Hour
	MaxJitter       = time.Hour
	MaxPerServer    = 50
	maxNameLen      = 100
	defaultGrace    = 5 * time.Minute
	defaultMaxSleep = time.Minute
)

// Event types.
const (
	EventCreated     = "schedule.created"
	EventUpdated     = "schedule.updated"
	EventDeleted     = "schedule.deleted"
	EventRunQueued   = "schedule.run.queued"
	EventRunSkipped  = "schedule.run.skipped"
	EventRunFinished = "schedule.run.finished"
)

// Errors.
var (
	ErrNotFound = errors.New("schedule not found")
	ErrInvalid  = errors.New("invalid schedule")
	ErrRunning  = errors.New("this schedule is already running")
	ErrTooMany  = fmt.Errorf("a server can have at most %d schedules", MaxPerServer)
)

// Step is one step of a run.
type Step struct {
	Type     string             `json:"type"`
	Command  string             `json:"command,omitempty"`  // command
	Action   server.PowerAction `json:"action,omitempty"`   // power
	Duration server.Duration    `json:"duration,omitempty"` // wait
	// ContinueOnFailure: a failed step doesn't stop the run. (A console
	// command fails when the server is offline, for example.)
	ContinueOnFailure bool `json:"continue_on_failure,omitempty"`
}

func (s Step) String() string {
	switch s.Type {
	case StepCommand:
		return fmt.Sprintf("command %q", s.Command)
	case StepPower:
		return string(s.Action)
	case StepWait:
		return "wait " + time.Duration(s.Duration).String()
	}
	return s.Type
}

// Definition is a schedule as the Panel sends it.
type Definition struct {
	Name     string `json:"name"`
	Cron     string `json:"cron"`               // five fields, or @hourly, @daily, …
	Timezone string `json:"timezone,omitempty"` // IANA name; "" = UTC
	Enabled  bool   `json:"enabled"`
	// OnlyWhenOnline: scheduled runs are skipped while the server isn't
	// running. ("Run now" always runs.)
	OnlyWhenOnline bool `json:"only_when_online,omitempty"`
	// Jitter delays every run by the same amount, between zero and this,
	// picked from the schedule's ID, so schedules set for the same minute on
	// one node don't all start at once.
	Jitter server.Duration `json:"jitter,omitempty"`
	Missed string          `json:"missed,omitempty"` // MissedSkip (default) or MissedRunOnce
	Steps  []Step          `json:"steps"`
}

// validate checks a definition and fills in defaults.
func (d *Definition) validate() (*cron.Schedule, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxNameLen {
		return nil, fmt.Errorf("name must be 1 to %d characters", maxNameLen)
	}
	if d.Timezone == "" {
		d.Timezone = "UTC"
	}
	c, err := cron.Parse(d.Cron, d.Timezone)
	if err != nil {
		return nil, err
	}
	if d.Jitter < 0 || time.Duration(d.Jitter) > MaxJitter {
		return nil, fmt.Errorf("jitter must be between 0 and %s", MaxJitter)
	}
	switch d.Missed {
	case "":
		d.Missed = MissedSkip
	case MissedSkip, MissedRunOnce:
	default:
		return nil, fmt.Errorf("missed must be %q or %q", MissedSkip, MissedRunOnce)
	}
	if len(d.Steps) == 0 || len(d.Steps) > MaxSteps {
		return nil, fmt.Errorf("a schedule needs 1 to %d steps", MaxSteps)
	}
	for i, s := range d.Steps {
		if err := s.validate(); err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return c, nil
}

func (s Step) validate() error {
	switch s.Type {
	case StepCommand:
		if strings.TrimSpace(s.Command) == "" {
			return errors.New("command is empty")
		}
		return server.CheckCommand(s.Command)
	case StepWait:
		if s.Duration < server.Duration(time.Second) || time.Duration(s.Duration) > MaxWait {
			return fmt.Errorf("wait must be between 1s and %s", MaxWait)
		}
	case StepPower:
		switch s.Action {
		case server.PowerStart, server.PowerStop, server.PowerRestart, server.PowerKill:
		default:
			return fmt.Errorf("unknown power action %q", s.Action)
		}
	case StepBackup:
	default:
		return fmt.Errorf("unknown step type %q", s.Type)
	}
	return nil
}

// Schedule is a stored schedule.
type Schedule struct {
	ID       string
	ServerID string
	Definition
	NextRun time.Time // zero when disabled
	LastRun time.Time // zero if it never ran
	Version int64
}

// offset is the schedule's jitter: the same fraction of Jitter every run.
func (s *Schedule) offset() time.Duration {
	if s.Jitter < server.Duration(time.Second) {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.ID))
	secs := uint64(time.Duration(s.Jitter) / time.Second) //nolint:gosec // validated: 1s to 1h
	return time.Duration(h.Sum64()%secs) * time.Second    //nolint:gosec // less than an hour
}

// next returns the first run after now (zero if disabled).
func (s *Schedule) next(c *cron.Schedule, now time.Time) time.Time {
	if !s.Enabled {
		return time.Time{}
	}
	off := s.offset()
	n := c.Next(now.Add(-off))
	if n.IsZero() {
		return n
	}
	return n.Add(off)
}

func (s *Schedule) eventData() map[string]any {
	d := map[string]any{"schedule_id": s.ID, "definition": s.Definition, "version": s.Version}
	if !s.NextRun.IsZero() {
		d["next_run_at"] = s.NextRun.UnixMilli()
	}
	return d
}

func fromRow(r store.Schedule) (*Schedule, error) {
	s := &Schedule{
		ID: r.ID, ServerID: r.ServerID, Version: r.Version,
		Definition: Definition{
			Name: r.Name, Cron: r.Cron, Timezone: r.Timezone, Enabled: r.Enabled == 1,
			OnlyWhenOnline: r.OnlyWhenOnline == 1, Jitter: server.Duration(time.Duration(r.JitterS) * time.Second),
			Missed: r.Missed,
		},
	}
	if err := json.Unmarshal([]byte(r.Steps), &s.Steps); err != nil {
		return nil, fmt.Errorf("schedule %s: steps: %w", r.ID, err)
	}
	if r.NextRunAt.Valid {
		s.NextRun = time.UnixMilli(r.NextRunAt.Int64)
	}
	if r.LastRunAt.Valid {
		s.LastRun = time.UnixMilli(r.LastRunAt.Int64)
	}
	return s, nil
}

func nullMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Create adds a schedule to a server.
func (s *Scheduler) Create(ctx context.Context, serverID string, d Definition) (*Schedule, error) {
	if _, err := s.o.Servers.Status(serverID); err != nil {
		return nil, err
	}
	var sc *Schedule
	err := s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		var err error
		sc, err = s.CreateTx(ctx, q, serverID, d)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return sc, nil
}

// CreateTx adds a schedule in a transaction (a new server's default backup
// schedule, stored with the server). The scheduler notices it within
// MaxSleep.
func (s *Scheduler) CreateTx(ctx context.Context, q *store.Queries, serverID string, d Definition) (*Schedule, error) {
	c, err := d.validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := s.now()
	sc := &Schedule{ID: id.String(), ServerID: serverID, Definition: d, Version: 1}
	sc.NextRun = sc.next(c, now)
	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return nil, err
	}
	n, err := q.CountServerSchedules(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if n >= MaxPerServer {
		return nil, ErrTooMany
	}
	if err := q.InsertSchedule(ctx, store.InsertScheduleParams{
		ID: sc.ID, ServerID: serverID, Name: d.Name, Cron: d.Cron, Timezone: d.Timezone,
		Enabled: boolInt(d.Enabled), OnlyWhenOnline: boolInt(d.OnlyWhenOnline),
		JitterS: int64(time.Duration(d.Jitter) / time.Second), Missed: d.Missed, Steps: string(steps),
		NextRunAt: nullMillis(sc.NextRun), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}); err != nil {
		return nil, err
	}
	_, err = events.AppendTx(ctx, q, events.Event{Type: EventCreated, ServerID: serverID, Data: sc.eventData()})
	return sc, err
}

// DefaultBackup is the schedule a new server gets unless it opts out: a
// daily backup between 04:00 and 05:00 in tz (the jitter spreads a node's
// servers over the hour), made up once if the node was off at the time,
// and skipped while the server is offline (its files aren't changing).
func DefaultBackup(tz string) Definition {
	return Definition{
		Name: "Daily backup", Cron: "0 4 * * *", Timezone: tz, Enabled: true, OnlyWhenOnline: true,
		Jitter: server.Duration(time.Hour), Missed: MissedRunOnce, Steps: []Step{{Type: StepBackup}},
	}
}

// Update replaces a schedule's definition. The next run is worked out again
// from now.
func (s *Scheduler) Update(ctx context.Context, serverID, id string, d Definition) (*Schedule, error) {
	c, err := d.validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	sc, err := s.Get(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sc.Definition = d
	sc.Version++
	sc.NextRun = sc.next(c, now)
	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return nil, err
	}
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.UpdateSchedule(ctx, store.UpdateScheduleParams{
			Name: d.Name, Cron: d.Cron, Timezone: d.Timezone, Enabled: boolInt(d.Enabled),
			OnlyWhenOnline: boolInt(d.OnlyWhenOnline), JitterS: int64(time.Duration(d.Jitter) / time.Second),
			Missed: d.Missed, Steps: string(steps), NextRunAt: nullMillis(sc.NextRun), UpdatedAt: now.UnixMilli(), ID: id,
		}); err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventUpdated, ServerID: serverID, Data: sc.eventData()})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return sc, nil
}

// Delete removes a schedule, cancelling a run in progress.
func (s *Scheduler) Delete(ctx context.Context, serverID, id string) error {
	if _, err := s.Get(ctx, serverID, id); err != nil {
		return err
	}
	if err := s.cancelRuns(ctx, id); err != nil {
		return err
	}
	err := s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.DeleteSchedule(ctx, id); err != nil {
			return err
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventDeleted, ServerID: serverID, Data: map[string]any{"schedule_id": id}})
		return err
	})
	if err != nil {
		return err
	}
	s.changed()
	return nil
}

// Get returns one of a server's schedules. A schedule of another server is
// not found: Panel grants are per server.
func (s *Scheduler) Get(ctx context.Context, serverID, id string) (*Schedule, error) {
	r, err := s.o.Store.Read.GetSchedule(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && r.ServerID != serverID) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromRow(r)
}

// List returns a server's schedules, or every schedule ("").
func (s *Scheduler) List(ctx context.Context, serverID string) ([]*Schedule, error) {
	rows, err := s.o.Store.Read.ListSchedules(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]*Schedule, 0, len(rows))
	for _, r := range rows {
		sc, err := fromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}
