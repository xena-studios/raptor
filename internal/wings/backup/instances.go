package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Database instances are backed up on their own (docs/DATABASES.md#backups,
// DECISIONS #343), so a database no server uses still has backups: a dump
// of every database on the instance, taken as each database's own user,
// snapshotted through the same destinations, jobs, and retention as servers'
// backups. Their rows are in backups with subject "instance" and the
// instance's ID in server_id; their events are db.backup.*. Each instance
// has one destination and keeps its last KeepLast backups there, backed up
// daily at a time of day in the node's time zone.

// JobInstance takes a database instance's backup.
const JobInstance = "backup.instance"

// EventInstancePolicy: an instance's backup settings changed.
const EventInstancePolicy = "db.backup.policy.updated"

// Instance backup defaults: daily at 04:00 like servers' default schedule,
// keeping a week of them.
const (
	DefaultInstanceKeep = 7
	DefaultInstanceAt   = "04:00"
	maxInstanceKeep     = 100
	instanceTick        = time.Minute
)

// Instances is what instance backups need from the databases.
type Instances interface {
	// DumpInstance dumps every database on an instance into a new folder,
	// one file each named after the database (its db_name, with the
	// engine's extension), and returns it with the databases' names.
	DumpInstance(ctx context.Context, id string, log io.Writer) (dir string, names []string, err error)
	// InstanceIDs lists the node's instances.
	InstanceIDs(ctx context.Context) ([]string, error)
	// InstanceName returns an instance's name and engine, for events.
	InstanceName(ctx context.Context, id string) (name, engine string)
}

// InstancePolicy is an instance's backup settings.
type InstancePolicy struct {
	// Enabled turns the daily backup on; backups can be taken by hand
	// either way.
	Enabled bool `json:"enabled"`
	// At is the time of day, HH:MM in the node's time zone.
	At            string `json:"at"`
	KeepLast      int    `json:"keep_last"`
	DestinationID string `json:"destination_id"`
}

func (p *InstancePolicy) validate() error {
	if _, err := time.Parse("15:04", p.At); err != nil || len(p.At) != 5 {
		return errors.New("the time is HH:MM, like 04:00")
	}
	if p.KeepLast < 1 || p.KeepLast > maxInstanceKeep {
		return fmt.Errorf("keep between 1 and %d backups", maxInstanceKeep)
	}
	if p.DestinationID == "" {
		p.DestinationID = LocalDestination
	}
	return nil
}

// KeepsLess reports whether p lets retention delete backups cur keeps.
func (p InstancePolicy) KeepsLess(cur InstancePolicy) bool {
	return p.DestinationID == cur.DestinationID && p.KeepLast < cur.KeepLast
}

// defaultInstancePolicy is an instance's settings until they're saved: its
// backups go offsite if the node backs up anywhere offsite (the first such
// destination), so they outlive the node.
func (m *Manager) defaultInstancePolicy(ctx context.Context) (InstancePolicy, error) {
	p := InstancePolicy{Enabled: true, At: DefaultInstanceAt, KeepLast: DefaultInstanceKeep, DestinationID: LocalDestination}
	dests, err := m.o.Store.Read.ListBackupDestinations(ctx)
	if err != nil {
		return p, err
	}
	for _, d := range dests {
		if d.ID != LocalDestination && d.ReadOnly == 0 {
			p.DestinationID = d.ID
			break
		}
	}
	return p, nil
}

// InstancePolicy returns an instance's backup settings.
func (m *Manager) InstancePolicy(ctx context.Context, instanceID string) (InstancePolicy, error) {
	r, err := m.o.Store.Read.GetInstanceBackupPolicy(ctx, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return m.defaultInstancePolicy(ctx)
	}
	if err != nil {
		return InstancePolicy{}, err
	}
	return InstancePolicy{Enabled: r.Enabled == 1, At: r.At, KeepLast: int(r.KeepLast), DestinationID: r.DestinationID}, nil
}

// SetInstancePolicy saves an instance's backup settings. Backups already
// taken stay where they are; retention applies to them from the next one.
func (m *Manager) SetInstancePolicy(ctx context.Context, instanceID string, p InstancePolicy) error {
	if err := p.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := m.instanceExists(ctx, instanceID); err != nil {
		return err
	}
	now := m.o.Now().UnixMilli()
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		d, err := q.GetBackupDestination(ctx, p.DestinationID)
		if err != nil {
			return ErrDestination
		}
		if d.ReadOnly == 1 {
			return fmt.Errorf("%w: %q holds recovered backups and is read-only", ErrInvalid, d.Name)
		}
		// A first save doesn't make a backup at once: the next one is at
		// the time chosen.
		if err := q.UpsertInstanceBackupPolicy(ctx, store.UpsertInstanceBackupPolicyParams{
			InstanceID: instanceID, Enabled: boolInt(p.Enabled), At: p.At, KeepLast: int64(p.KeepLast),
			DestinationID: p.DestinationID, LastRunAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventInstancePolicy, Data: map[string]any{
			"instance_id": instanceID, "policy": p,
		}})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

func (m *Manager) instanceExists(ctx context.Context, id string) error {
	if m.o.Instances == nil {
		return errors.New("database instances aren't backed up on this node")
	}
	ids, err := m.o.Instances.InstanceIDs(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, id) {
		return fmt.Errorf("%w: no database instance %s", ErrNotFound, id)
	}
	return nil
}

// InstanceBackups returns an instance's backups, newest first.
func (m *Manager) InstanceBackups(ctx context.Context, instanceID string) ([]*Backup, error) {
	rows, err := m.o.Store.Read.ListInstanceBackups(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	out := make([]*Backup, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// InstanceBackup returns one of an instance's backups.
func (m *Manager) InstanceBackup(ctx context.Context, instanceID, id string) (*Backup, error) {
	r, err := m.o.Store.Read.GetBackup(ctx, id)
	if err != nil || r.Subject != SubjectInstance || r.ServerID != instanceID {
		return nil, ErrNotFound
	}
	return fromRow(r), nil
}

type instancePayload struct {
	BackupID string `json:"backup_id"`
}

// BackupInstance queues a backup of an instance to its destination.
func (m *Manager) BackupInstance(ctx context.Context, instanceID, kind, user string) (*Backup, error) {
	if kind != KindManual && kind != KindScheduled {
		return nil, fmt.Errorf("%w: kind %q", ErrInvalid, kind)
	}
	if err := m.instanceExists(ctx, instanceID); err != nil {
		return nil, err
	}
	pol, err := m.InstancePolicy(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if err := m.checkSpace(pol.DestinationID); err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	b := &Backup{
		ID: id, ServerID: instanceID, Subject: SubjectInstance, DestinationID: pol.DestinationID, Kind: kind,
		Status: StatusPending, CreatedBy: user, CreatedAt: m.o.Now(),
	}
	b.instance, b.engine = m.o.Instances.InstanceName(ctx, instanceID)
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		jobID, err := m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobInstance, Payload: instancePayload{BackupID: id}})
		if err != nil {
			return err
		}
		b.JobID = jobID
		return m.insert(ctx, q, b)
	})
	if err != nil {
		return nil, err
	}
	m.o.Events.Wake()
	m.o.Jobs.Wake()
	return b, nil
}

func (m *Manager) instanceJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p instancePayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	b, err := m.row(ctx, p.BackupID)
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // deleted meanwhile (its destination or instance went)
	}
	b.instance, b.engine = m.o.Instances.InstanceName(ctx, b.ServerID)
	if b.Status == StatusOK {
		return map[string]any{"backup_id": b.ID}, nil
	}
	res, err := m.snapshotInstance(ctx, b, log)
	if err != nil {
		return nil, err
	}
	pol, err := m.InstancePolicy(ctx, b.ServerID)
	if err == nil && pol.DestinationID == b.DestinationID {
		if err := m.retention(ctx, b.ServerID, b.DestinationID, Retention{KeepLast: pol.KeepLast}, log); err != nil {
			_, _ = fmt.Fprintln(log, "retention failed:", err)
			m.log.Warn("instance backup retention failed", "instance", b.ServerID, "destination", b.DestinationID, "err", err)
		}
	}
	return map[string]any{"backup_id": b.ID, "destination_id": b.DestinationID, "size": res.Size, "files": res.Files, "uploaded": res.Uploaded}, nil
}

// snapshotInstance dumps an instance's databases and snapshots the dumps
// for b, recording how it went.
func (m *Manager) snapshotInstance(ctx context.Context, b *Backup, log io.Writer) (engine.SnapshotResult, error) {
	var res engine.SnapshotResult
	if m.o.Instances == nil {
		return res, errors.New("database instances aren't backed up on this node")
	}
	if err := m.o.Store.Write.SetBackupRunning(ctx, b.ID); err != nil {
		return res, err
	}
	dir, names, err := m.o.Instances.DumpInstance(ctx, b.ServerID, log)
	if err != nil {
		if !errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			m.fail(ctx, b, err)
		}
		return res, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	b.Databases = names
	warning := ""
	if len(names) == 0 {
		warning = "the instance has no databases"
	}
	_, _ = fmt.Fprintf(log, "backing up to %s\n", b.DestinationID)
	out, err := m.run(ctx, b.DestinationID, request{Op: opSnapshot, Snapshot: &engine.SnapshotRequest{
		ServerID: b.ServerID, BackupID: b.ID, Dir: dir, Instance: true,
	}}, progressLog(log), log)
	if err != nil {
		if errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			return res, err
		}
		m.recordOutcome(ctx, b.DestinationID, err)
		err = Explain(err)
		m.fail(ctx, b, err)
		return res, err
	}
	m.recordOutcome(ctx, b.DestinationID, nil)
	if err := json.Unmarshal(out, &res); err != nil {
		return res, err
	}
	if err := m.finish(ctx, b, res, warning); err != nil {
		return res, err
	}
	_, _ = fmt.Fprintf(log, "done: %d databases, %d bytes, %d bytes new\n", len(names), res.Size, res.Uploaded)
	return res, nil
}

// InstanceJobBackup backs up an instance for a job that's about to change
// or delete it: a restore's safety backup (KindSafety) or a deletion's
// final backup (KindFinal). It runs inside that job; a resumed job gets the
// backup it already finished.
func (m *Manager) InstanceJobBackup(ctx context.Context, instanceID, jobID, kind string, log io.Writer) (string, error) {
	if kind != KindSafety && kind != KindFinal {
		return "", fmt.Errorf("%w: kind %q", ErrInvalid, kind)
	}
	var b *Backup
	if r, err := m.o.Store.Write.GetJobBackup(ctx, store.GetJobBackupParams{JobID: jobID, Kind: kind}); err == nil {
		b = fromRow(r)
		if b.Status == StatusOK {
			return b.ID, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if m.o.Instances == nil {
		return "", errors.New("database instances aren't backed up on this node")
	}
	pol, err := m.InstancePolicy(ctx, instanceID)
	if err != nil {
		return "", err
	}
	if b == nil {
		if err := m.checkSpace(pol.DestinationID); err != nil {
			return "", err
		}
		id, err := newID()
		if err != nil {
			return "", err
		}
		now := m.o.Now()
		b = &Backup{
			ID: id, ServerID: instanceID, Subject: SubjectInstance, DestinationID: pol.DestinationID, Kind: kind,
			Status: StatusPending, JobID: jobID, CreatedBy: hookUser, CreatedAt: now,
		}
		// Like a server's: a safety backup expires; a final one on the
		// node's own disk expires too, offsite it's kept.
		switch {
		case kind == KindSafety:
			b.ExpiresAt = now.Add(SafetyTTL)
		case pol.DestinationID == LocalDestination:
			b.ExpiresAt = now.Add(FinalTTL)
		}
		if err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error { return m.insert(ctx, q, b) }); err != nil {
			return "", err
		}
		m.o.Events.Wake()
	}
	b.instance, b.engine = m.o.Instances.InstanceName(ctx, instanceID)
	if kind == KindFinal {
		_, _ = fmt.Fprintf(log, "taking a final backup to %s\n", b.DestinationID)
	} else {
		_, _ = fmt.Fprintf(log, "taking a safety backup to %s\n", b.DestinationID)
	}
	if _, err := m.snapshotInstance(ctx, b, log); err != nil {
		return "", err
	}
	return b.ID, nil
}

// DeleteInstanceBackup queues deleting one of an instance's backups.
func (m *Manager) DeleteInstanceBackup(ctx context.Context, instanceID, id string) (string, error) {
	b, err := m.InstanceBackup(ctx, instanceID, id)
	if err != nil {
		return "", err
	}
	if b.Status == StatusPending || b.Status == StatusRunning {
		return "", ErrNotReady
	}
	return m.enqueueDelete(ctx, b.DestinationID, []string{id}, "deleted")
}

// ExtractInstanceDatabase takes an instance backup's dump of one database
// out into a new folder inside dir, and returns the dump's path. It runs
// inside the caller's job.
func (m *Manager) ExtractInstanceDatabase(ctx context.Context, instanceID, backupID, name, file, dir string, log io.Writer) (string, error) {
	b, err := m.InstanceBackup(ctx, instanceID, backupID)
	if err != nil {
		return "", err
	}
	if b.Status != StatusOK {
		return "", ErrNotReady
	}
	if !slices.Contains(b.Databases, name) {
		return "", fmt.Errorf("%w: that backup doesn't hold the database %s", ErrNotFound, name)
	}
	if _, err := m.run(ctx, b.DestinationID, request{Op: opExtract, Extract: &engine.ExtractRequest{
		SnapshotID: b.snapshotID, Dir: dir, Into: "dump", Paths: []string{file}, UID: -1, GID: -1,
	}}, progressLog(log), log); err != nil {
		return "", err
	}
	return filepath.Join(dir, "dump", filepath.FromSlash(file)), nil
}

// InstanceDeleted deletes a deleted instance's backups on the node's own
// disk, except its final backup (which expires after FinalTTL). Offsite
// ones are kept, like a deleted server's.
func (m *Manager) InstanceDeleted(ctx context.Context, instanceID string) {
	rows, err := m.o.Store.Read.ListDestinationBackups(ctx, store.ListDestinationBackupsParams{DestinationID: LocalDestination, ServerID: instanceID})
	if err != nil {
		m.log.Error("listing a deleted instance's backups failed", "instance", instanceID, "err", err)
		return
	}
	var ids []string
	for _, r := range rows {
		if r.Subject == SubjectInstance && r.Kind != KindFinal {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := m.enqueueDelete(ctx, LocalDestination, ids, "instance_deleted"); err != nil {
		m.log.Error("queueing a deleted instance's backups for deletion failed", "instance", instanceID, "err", err)
	}
}

func (m *Manager) instanceLoop() {
	t := time.NewTicker(instanceTick)
	defer t.Stop()
	for {
		m.scheduleInstances(m.ctx)
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// scheduleInstances queues each instance's daily backup once its time has
// come today and it hasn't run since. One missed while the node was off is
// made up once, when Wings is back. An instance seen for the first time
// gets the default settings, its first backup at their time.
func (m *Manager) scheduleInstances(ctx context.Context) {
	ids, err := m.o.Instances.InstanceIDs(ctx)
	if err != nil {
		m.log.Error("listing database instances for backups failed", "err", err)
		return
	}
	now := m.o.Now()
	for _, id := range ids {
		r, err := m.o.Store.Read.GetInstanceBackupPolicy(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			p, err := m.defaultInstancePolicy(ctx)
			if err == nil {
				err = m.SetInstancePolicy(ctx, id, p)
			}
			if err != nil {
				m.log.Error("saving an instance's default backup settings failed", "instance", id, "err", err)
			}
			continue
		}
		if err != nil {
			m.log.Error("reading an instance's backup settings failed", "instance", id, "err", err)
			continue
		}
		if r.Enabled == 0 || !due(r.At, r.LastRunAt, now, m.o.Location) {
			continue
		}
		if err := m.o.Store.Write.SetInstanceBackupRun(ctx, store.SetInstanceBackupRunParams{LastRunAt: now.UnixMilli(), InstanceID: id}); err != nil {
			m.log.Error("recording an instance's backup run failed", "instance", id, "err", err)
			continue
		}
		if _, err := m.BackupInstance(ctx, id, KindScheduled, "schedule"); err != nil {
			m.log.Warn("queueing an instance's daily backup failed", "instance", id, "err", err)
		}
	}
}

// due reports whether a daily backup at "HH:MM" (in loc) should run now,
// having last run at lastRun (unix ms): its time today has passed and it
// hasn't run since.
func due(at string, lastRun int64, now time.Time, loc *time.Location) bool {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return false
	}
	n := now.In(loc)
	slot := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	return !n.Before(slot) && lastRun < slot.UnixMilli()
}
