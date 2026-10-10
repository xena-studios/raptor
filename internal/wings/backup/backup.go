// Package backup runs server backups (docs/WINGS.md#backups): Kopia
// repositories on local disk or S3-compatible storage, one per destination,
// shared by every server on the node; backups and restores as jobs, with the
// egg's hooks around them; retention after every backup; and repository
// maintenance in the background.
package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Backup kinds.
const (
	KindManual    = "manual"
	KindScheduled = "scheduled"
	KindSafety    = "safety" // taken before a restore or a wipe; expires after SafetyTTL
	KindFinal     = "final"  // taken before a server is deleted; kept (FinalTTL on the local destination)
	// KindRecovered: found on a recovered destination (another node's, or
	// this node's in the past); restored from, never deleted by Raptor.
	KindRecovered = "recovered"
)

// Backup statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

// Job types.
const (
	JobCreate   = "backup.create"
	JobRestore  = "backup.restore"
	JobDelete   = "backup.delete"
	JobMaintain = "backup.maintain"
	JobExtract  = "backup.extract"
)

// Event types.
const (
	EventQueued        = "backup.queued"
	EventExtracted     = "backup.extract.finished"
	EventFinished      = "backup.finished" // ok or failed
	EventDeleted       = "backup.deleted"
	EventLocked        = "backup.locked"
	EventRestoreQueued = "backup.restore.queued"
	EventRestored      = "backup.restore.finished"
	EventPolicy        = "backup.policy.updated"
	EventDestination   = "backup.destination.updated" // created, updated, or deleted
	// EventDestinationStatus: a destination failed, or worked again after
	// failing.
	EventDestinationStatus = "backup.destination.status"
)

// LocalDestination is the ID of the built-in local destination.
const LocalDestination = "local"

// Timings.
const (
	SafetyTTL = 7 * 24 * time.Hour
	// FinalTTL is how long a deleted server's final backup stays on the
	// local destination (host disk). Offsite ones are kept until deleted.
	FinalTTL         = 30 * 24 * time.Hour
	failedTTL        = 7 * 24 * time.Hour
	maintainInterval = time.Hour
	// measureInterval is how often a destination's size is added up:
	// listing a bucket isn't free.
	measureInterval = 24 * time.Hour
	maxIgnore       = 100
	maxNameLen      = 100
)

// passwordKey holds the repository password in kv. One password per node,
// for every destination.
const passwordKey = "backup.password"

// Errors.
var (
	ErrNotFound    = errors.New("backup not found")
	ErrInvalid     = errors.New("invalid backup settings")
	ErrNotReady    = errors.New("the backup isn't finished")
	ErrInUse       = errors.New("destination is used by servers' backup settings")
	ErrLowDisk     = errors.New("not enough free disk space for backups")
	ErrDestination = errors.New("destination not found")
	ErrReadOnly    = errors.New("recovered backups aren't deleted by Raptor; remove the recovered destination to forget them")
)

// Servers is what backups need from the server manager.
type Servers interface {
	Status(id string) (server.Status, error)
	SendCommand(id, user, cmd string) error
	BackupSource(ctx context.Context, id string) (server.BackupSource, error)
	Restore(ctx context.Context, id string, start bool, fn func(ctx context.Context, dir string) error) error
}

// Options configure a Manager.
type Options struct {
	Store   *store.DB
	Jobs    *jobs.Engine
	Events  *events.Outbox
	Servers Servers
	Log     *slog.Logger
	Runner  Runner
	// LocalPath is the local destination's directory.
	LocalPath string
	// ReservedPaths are directories a folder destination can't be in or
	// contain (Raptor's data and server volumes), beside the system's.
	ReservedPaths []string
	// NodeID is this node's, for Raptor Backup Storage's folder ("" when
	// not linked: no hosted storage).
	NodeID string
	// FolderRoots are where folder destinations may be (default /mnt,
	// /media, /srv: what the service may write).
	FolderRoots []string
	// StateDir holds repository connections and caches.
	StateDir string
	// Location is the time zone retention counts days, weeks, and months
	// in (default UTC).
	Location *time.Location
	// FreeSpace returns the free bytes on the filesystem holding a path; with
	// MinFree, backups to the local destination are refused below it. nil =
	// no check.
	FreeSpace func(path string) (int64, error)
	MinFree   int64
	// HookTimeout is how long to wait for the egg's wait_for line (default
	// 1 minute).
	HookTimeout time.Duration
	Now         func() time.Time
}

// Manager runs backups.
type Manager struct {
	o   Options
	log *slog.Logger

	pwMu sync.Mutex
	pw   string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	acts activities
}

// New creates a Manager and registers its job handlers. Call it before
// Jobs.Start, and Start after.
func New(o Options) *Manager {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Runner == nil {
		o.Runner = InProcess{}
	}
	if o.HookTimeout == 0 {
		o.HookTimeout = time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{o: o, log: o.Log, ctx: ctx, cancel: cancel}
	// Backups are resumable: a backup interrupted by a Wings stop starts
	// again (it's incremental, so the work already uploaded isn't redone).
	o.Jobs.Register(JobCreate, jobs.Handler{Class: "backup", ServerLock: true, Resumable: true, MaxAttempts: 3, Run: m.createJob})
	// A restore resumes too: it clears the directory and restores again.
	o.Jobs.Register(JobRestore, jobs.Handler{Class: "backup", ServerLock: true, Resumable: true, MaxAttempts: 3, Run: m.restoreJob})
	o.Jobs.Register(JobDelete, jobs.Handler{Class: "backup", Resumable: true, MaxAttempts: 5, Run: m.deleteJob})
	o.Jobs.Register(JobMaintain, jobs.Handler{Class: "backup", Resumable: true, Run: m.maintainJob})
	o.Jobs.Register(JobScan, jobs.Handler{Class: "backup", Resumable: true, MaxAttempts: 3, Run: m.scanJob})
	// An extraction isn't resumed: its folder would already exist. It holds
	// the server's lock so a restore can't clear the directory under it.
	o.Jobs.Register(JobExtract, jobs.Handler{Class: "backup", ServerLock: true, MaxAttempts: 1, Run: m.extractJob})
	return m
}

// Start cleans up after the last run and starts background maintenance.
func (m *Manager) Start(ctx context.Context) error {
	if err := m.cleanupInterrupted(ctx); err != nil {
		return err
	}
	m.wg.Go(m.loop)
	return nil
}

// Close stops background maintenance. Running jobs are the job engine's.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

func (m *Manager) loop() {
	t := time.NewTicker(maintainInterval)
	defer t.Stop()
	for {
		m.housekeeping(m.ctx)
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// housekeeping deletes expired backups and queues maintenance for every
// destination with backups.
func (m *Manager) housekeeping(ctx context.Context) {
	now := m.o.Now()
	expired, err := m.o.Store.Read.ExpiredBackups(ctx, store.ExpiredBackupsParams{
		Now: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, FailedBefore: now.Add(-failedTTL).UnixMilli(),
	})
	if err != nil {
		m.log.Error("listing expired backups failed", "err", err)
	}
	byDest := map[string][]string{}
	for _, b := range expired {
		byDest[b.DestinationID] = append(byDest[b.DestinationID], b.ID)
	}
	for dest, ids := range byDest {
		if _, err := m.enqueueDelete(ctx, dest, ids, "expired"); err != nil {
			m.log.Error("queueing expired backup deletion failed", "err", err)
		}
	}
	dests, err := m.o.Store.Read.ListBackupDestinations(ctx)
	if err != nil {
		m.log.Error("listing backup destinations failed", "err", err)
		return
	}
	active, err := m.activeJobs(ctx, JobMaintain)
	if err != nil {
		m.log.Error("listing backup jobs failed", "err", err)
		return
	}
	for _, d := range dests {
		if active[d.ID] || d.ReadOnly == 1 {
			continue // recovered destinations are never written to
		}
		measure := !d.SizeAt.Valid || now.Sub(time.UnixMilli(d.SizeAt.Int64)) >= measureInterval
		if _, err := m.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobMaintain, Payload: maintainPayload{DestinationID: d.ID, Measure: measure}}); err != nil {
			m.log.Error("queueing backup maintenance failed", "destination", d.ID, "err", err)
		}
	}
}

// activeJobs returns the destinations with a queued or running job of typ.
func (m *Manager) activeJobs(ctx context.Context, typ string) (map[string]bool, error) {
	list, err := m.o.Jobs.List(ctx, "", 500)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, j := range list {
		if j.Type != typ || (j.Status != jobs.Queued && j.Status != jobs.Running) {
			continue
		}
		var p maintainPayload
		if j.Decode(&p) == nil {
			out[p.DestinationID] = true
		}
	}
	return out, nil
}

// cleanupInterrupted fails backups whose job is gone (cancelled when their
// server was deleted, or given up on after repeated Wings crashes).
func (m *Manager) cleanupInterrupted(ctx context.Context) error {
	list, err := m.o.Store.Read.ListBackups(ctx, "")
	if err != nil {
		return err
	}
	for _, b := range list {
		if b.Status != StatusPending && b.Status != StatusRunning {
			continue
		}
		j, err := m.o.Jobs.Get(ctx, b.JobID)
		if err == nil && (j.Status == jobs.Queued || j.Status == jobs.Running) {
			continue
		}
		m.fail(ctx, fromRow(b), errors.New("interrupted"))
	}
	return nil
}

// password returns the node's repository password, creating it on first
// use.
func (m *Manager) password(ctx context.Context) (string, error) {
	m.pwMu.Lock()
	defer m.pwMu.Unlock()
	if m.pw != "" {
		return m.pw, nil
	}
	var pw string
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if v, err := q.GetKV(ctx, passwordKey); err == nil {
			pw = string(v)
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		pw = base64.RawURLEncoding.EncodeToString(b)
		return q.SetKV(ctx, store.SetKVParams{Key: passwordKey, Value: []byte(pw)})
	})
	if err != nil {
		return "", err
	}
	m.pw = pw
	return pw, nil
}

// --- backups ---

// Backup is a stored backup.
type Backup struct {
	ID            string    `json:"id"`
	ServerID      string    `json:"server_id"`
	DestinationID string    `json:"destination_id"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	Locked        bool      `json:"locked"`
	Size          int64     `json:"size"`
	Files         int64     `json:"files"`
	Uploaded      int64     `json:"uploaded"`
	Warning       string    `json:"warning,omitempty"`
	Error         string    `json:"error,omitempty"`
	JobID         string    `json:"job_id"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	FinishedAt    time.Time `json:"finished_at,omitzero"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"`
	snapshotID    string
}

func fromRow(r store.Backup) *Backup {
	b := &Backup{
		ID: r.ID, ServerID: r.ServerID, DestinationID: r.DestinationID, Kind: r.Kind, Status: r.Status,
		Locked: r.Locked == 1, Size: r.Size, Files: r.Files, Uploaded: r.Uploaded, Warning: r.Warning,
		Error: r.Error, JobID: r.JobID, CreatedBy: r.CreatedBy, CreatedAt: time.UnixMilli(r.CreatedAt),
		snapshotID: r.SnapshotID,
	}
	if r.FinishedAt.Valid {
		b.FinishedAt = time.UnixMilli(r.FinishedAt.Int64)
	}
	if r.ExpiresAt.Valid {
		b.ExpiresAt = time.UnixMilli(r.ExpiresAt.Int64)
	}
	return b
}

func (b *Backup) eventData() map[string]any {
	d := map[string]any{
		"backup_id": b.ID, "destination_id": b.DestinationID, "kind": b.Kind, "status": b.Status,
		"locked": b.Locked, "created_at": b.CreatedAt.UnixMilli(), "created_by": b.CreatedBy,
	}
	if b.Status == StatusOK {
		d["size"], d["files"], d["uploaded"] = b.Size, b.Files, b.Uploaded
	}
	if b.Warning != "" {
		d["warning"] = b.Warning
	}
	if b.Error != "" {
		d["error"] = b.Error
	}
	if !b.FinishedAt.IsZero() {
		d["finished_at"] = b.FinishedAt.UnixMilli()
	}
	if !b.ExpiresAt.IsZero() {
		d["expires_at"] = b.ExpiresAt.UnixMilli()
	}
	return d
}

// Restorable returns a backup that may be restored onto a server: one of
// its own, or one of a server that no longer exists on this node (a final
// backup, or an offsite backup kept after the deletion). Another existing
// server's backups are refused, so access to one server never reaches
// another's files. Restoring from a deleted server is the only way back
// after a deletion; only owners may do it (docs/SECURITY-MODEL.md).
func (m *Manager) Restorable(ctx context.Context, serverID, id string) (*Backup, error) {
	r, err := m.o.Store.Read.GetBackup(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	if r.ServerID != serverID {
		if _, err := m.o.Servers.Status(r.ServerID); err == nil {
			return nil, ErrNotFound
		}
	}
	return fromRow(r), nil
}

// FromDeletedServer reports whether restoring backup id onto serverID
// restores another (deleted) server's files.
func (m *Manager) FromDeletedServer(ctx context.Context, serverID, id string) (bool, error) {
	b, err := m.Restorable(ctx, serverID, id)
	if err != nil {
		return false, err
	}
	return b.ServerID != serverID, nil
}

// Get returns a server's backup.
func (m *Manager) Get(ctx context.Context, serverID, id string) (*Backup, error) {
	r, err := m.o.Store.Read.GetBackup(ctx, id)
	if err != nil || r.ServerID != serverID {
		return nil, ErrNotFound
	}
	return fromRow(r), nil
}

// List returns a server's backups, newest first ("" = every server's,
// including those of deleted servers).
func (m *Manager) List(ctx context.Context, serverID string) ([]*Backup, error) {
	rows, err := m.o.Store.Read.ListBackups(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]*Backup, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// CreateOptions describe a new backup.
type CreateOptions struct {
	Kind   string // KindManual or KindScheduled
	User   string // who asked, for the audit event
	Locked bool   // never deleted by retention
	// DestinationID backs up to one of the server's destinations; "" to
	// every one of them.
	DestinationID string
}

// ErrNotTarget means a backup was asked for to a destination the server's
// backups don't go to.
var ErrNotTarget = errors.New("this server's backups don't go to that destination; add it in the server's backup settings first")

// Create queues a backup of a server to each of its destinations (or the
// one asked for) and returns them, the primary first. They're one job: the
// snapshots run one after another.
func (m *Manager) Create(ctx context.Context, serverID string, opts CreateOptions) ([]*Backup, error) {
	if opts.Kind != KindManual && opts.Kind != KindScheduled {
		return nil, fmt.Errorf("%w: kind %q", ErrInvalid, opts.Kind)
	}
	if _, err := m.o.Servers.Status(serverID); err != nil {
		return nil, err
	}
	pol, err := m.Policy(ctx, serverID)
	if err != nil {
		return nil, err
	}
	targets := pol.Targets
	if opts.DestinationID != "" {
		t, ok := pol.Target(opts.DestinationID)
		if !ok {
			return nil, ErrNotTarget
		}
		targets = []Target{t}
	}
	for _, t := range targets {
		if err := m.checkSpace(t.DestinationID); err != nil {
			return nil, err
		}
	}
	var list []*Backup
	for _, t := range targets {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		list = append(list, &Backup{
			ID: id.String(), ServerID: serverID, DestinationID: t.DestinationID, Kind: opts.Kind,
			Status: StatusPending, Locked: opts.Locked, CreatedBy: opts.User, CreatedAt: m.o.Now(),
		})
	}
	ids := make([]string, len(list))
	for i, b := range list {
		ids[i] = b.ID
	}
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		jobID, err := m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobCreate, ServerID: serverID, Payload: createPayload{BackupIDs: ids}})
		if err != nil {
			return err
		}
		for _, b := range list {
			b.JobID = jobID
			if err := m.insert(ctx, q, b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		m.queued(serverID, Activity{JobID: b.JobID, Kind: ActBackup, BackupID: b.ID, BackupKind: b.Kind})
	}
	m.o.Events.Wake()
	m.o.Jobs.Wake()
	return list, nil
}

func (m *Manager) insert(ctx context.Context, q *store.Queries, b *Backup) error {
	var expires sql.NullInt64
	if !b.ExpiresAt.IsZero() {
		expires = sql.NullInt64{Int64: b.ExpiresAt.UnixMilli(), Valid: true}
	}
	if err := q.InsertBackup(ctx, store.InsertBackupParams{
		ID: b.ID, ServerID: b.ServerID, DestinationID: b.DestinationID, Kind: b.Kind, Locked: boolInt(b.Locked),
		JobID: b.JobID, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt.UnixMilli(), ExpiresAt: expires,
	}); err != nil {
		return err
	}
	_, err := events.AppendTx(ctx, q, events.Event{Type: EventQueued, ServerID: b.ServerID, Data: b.eventData()})
	return err
}

func (m *Manager) checkSpace(destID string) error {
	if destID != LocalDestination || m.o.FreeSpace == nil || m.o.MinFree <= 0 {
		return nil
	}
	free, err := m.o.FreeSpace(m.o.LocalPath)
	if err != nil {
		return nil //nolint:nilerr // the directory is created by the first backup
	}
	if free < m.o.MinFree {
		return fmt.Errorf("%w: %d bytes free on %s, at least %d needed", ErrLowDisk, free, m.o.LocalPath, m.o.MinFree)
	}
	return nil
}

// Backup queues a scheduled backup (a schedule's backup step) to the
// server's destinations, or one of them, and returns its job's ID.
func (m *Manager) Backup(ctx context.Context, serverID, user, destinationID string) (string, error) {
	list, err := m.Create(ctx, serverID, CreateOptions{Kind: KindScheduled, User: user, DestinationID: destinationID})
	if err != nil {
		return "", err
	}
	return list[0].JobID, nil
}

// Lock sets whether retention may delete a backup.
func (m *Manager) Lock(ctx context.Context, serverID, id string, locked bool) error {
	b, err := m.Get(ctx, serverID, id)
	if err != nil {
		return err
	}
	b.Locked = locked
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.SetBackupLocked(ctx, store.SetBackupLockedParams{Locked: boolInt(locked), ID: id}); err != nil {
			return err
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventLocked, ServerID: serverID, Data: b.eventData()})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

// Delete queues the deletion of a server's backup. A backup that's still
// being made can't be deleted.
func (m *Manager) Delete(ctx context.Context, serverID, id string) (string, error) {
	b, err := m.Get(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	if b.Status == StatusPending || b.Status == StatusRunning {
		return "", ErrNotReady
	}
	if b.Kind == KindRecovered {
		return "", ErrReadOnly
	}
	return m.enqueueDelete(ctx, b.DestinationID, []string{id}, "deleted")
}

// ServerDeleted deletes a deleted server's local backups, except its final
// backup (which expires after FinalTTL). Offsite ones are kept: they're the
// only way back (docs/SERVERS.md#deleting-a-server).
func (m *Manager) ServerDeleted(ctx context.Context, serverID string) {
	rows, err := m.o.Store.Read.ListDestinationBackups(ctx, store.ListDestinationBackupsParams{DestinationID: LocalDestination, ServerID: serverID})
	if err != nil {
		m.log.Error("listing a deleted server's backups failed", "server", serverID, "err", err)
		return
	}
	var ids []string
	for _, r := range rows {
		if r.Kind != KindFinal {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := m.enqueueDelete(ctx, LocalDestination, ids, "server_deleted"); err != nil {
		m.log.Error("queueing a deleted server's backup deletion failed", "server", serverID, "err", err)
	}
}

func (m *Manager) enqueueDelete(ctx context.Context, destID string, ids []string, reason string) (string, error) {
	id, err := m.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobDelete, Payload: deletePayload{DestinationID: destID, BackupIDs: ids, Reason: reason}})
	if err == nil {
		m.o.Jobs.Wake()
	}
	return id, err
}

// Restore queues restoring a backup over a server's files. A safety backup
// of the current files is taken first. The backup is the server's own, or
// one of a deleted server (see Restorable).
func (m *Manager) Restore(ctx context.Context, serverID, id, user string) (string, error) {
	b, err := m.Restorable(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	if b.Status != StatusOK {
		return "", ErrNotReady
	}
	var jobID string
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		jobID, err = m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobRestore, ServerID: serverID, Payload: restorePayload{BackupID: id, User: user}})
		if err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventRestoreQueued, ServerID: serverID, Data: map[string]any{
			"backup_id": id, "job_id": jobID, "user": user,
		}})
		return err
	})
	if err != nil {
		return "", err
	}
	m.queued(serverID, Activity{JobID: jobID, Kind: ActRestore, BackupID: id})
	m.o.Events.Wake()
	m.o.Jobs.Wake()
	return jobID, nil
}

// run sends one request to the worker.
func (m *Manager) run(ctx context.Context, destID string, req request, progress func(engine.Progress), log io.Writer) (json.RawMessage, error) {
	dest, err := m.engineDest(ctx, destID)
	if err != nil {
		return nil, err
	}
	pw, err := m.password(ctx)
	if err != nil {
		return nil, err
	}
	// A recovered destination's backups have their own key.
	if r, err := m.o.Store.Read.GetBackupDestination(ctx, destID); err == nil && r.RepoPassword != "" {
		pw = r.RepoPassword
	}
	req.Dest, req.Password, req.StateDir = dest, pw, m.o.StateDir
	return m.o.Runner.Run(ctx, req, progress, log)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}

// isEmpty reports whether a directory has no entries.
func isEmpty(dir string) (bool, error) {
	f, err := os.Open(dir) //nolint:gosec // the server's directory
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}
