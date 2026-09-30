package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

type createPayload struct {
	BackupID string `json:"backup_id"`
}

// createCheckpoint: the egg's pre-backup commands were sent, so a resumed
// job sends the post-backup ones (e.g. save-on) first.
type createCheckpoint struct {
	PreRan bool `json:"pre_ran"`
}

type restorePayload struct {
	BackupID string `json:"backup_id"`
	User     string `json:"user"`
}

// restoreCheckpoint is a restore's progress: the safety backup is taken
// once, and whether to start the server afterwards is decided before the
// first attempt stopped it.
type restoreCheckpoint struct {
	Start    bool   `json:"start"`
	SafetyID string `json:"safety_id,omitempty"` // "" = none needed (empty directory)
	Safety   bool   `json:"safety"`              // the safety backup is done
}

type deletePayload struct {
	DestinationID string   `json:"destination_id"`
	BackupIDs     []string `json:"backup_ids"`
	Reason        string   `json:"reason"` // deleted, retention, expired, server_deleted
}

type maintainPayload struct {
	DestinationID string `json:"destination_id"`
}

// hookUser is who the egg's backup commands are attributed to.
const hookUser = "backup"

func (m *Manager) row(ctx context.Context, id string) (*Backup, error) {
	r, err := m.o.Store.Read.GetBackup(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	return fromRow(r), nil
}

// createJob makes a backup: the egg's pre commands while the server is
// running, the snapshot, the post commands, then retention.
func (m *Manager) createJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p createPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	b, err := m.row(ctx, p.BackupID)
	if err != nil {
		return nil, err
	}
	src, err := m.o.Servers.BackupSource(ctx, b.ServerID)
	if err != nil {
		m.fail(ctx, b, err)
		return nil, err
	}
	var cp createCheckpoint
	if _, err := j.DecodeCheckpoint(&cp); err != nil {
		return nil, err
	}
	if cp.PreRan {
		_, _ = fmt.Fprintln(log, "resuming after a Wings restart; sending the post-backup commands first")
		m.post(b.ServerID, src, log)
	}
	pol, err := m.Policy(ctx, b.ServerID)
	if err != nil {
		return nil, err
	}
	if err := m.o.Store.Write.SetBackupRunning(ctx, b.ID); err != nil {
		return nil, err
	}

	var warning string
	pre := m.running(b.ServerID) && len(src.Pre) > 0
	if pre {
		cp.PreRan = true
		m.checkpoint(ctx, j, cp)
		warning = m.pre(ctx, b.ServerID, src, log)
	}
	_, _ = fmt.Fprintf(log, "backing up to %s\n", b.DestinationID)
	out, err := m.run(ctx, b.DestinationID, request{Op: opSnapshot, Snapshot: &engine.SnapshotRequest{
		ServerID: b.ServerID, BackupID: b.ID, Dir: src.Dir, Ignore: pol.Ignore,
	}}, progressLog(log), log)
	if pre {
		m.post(b.ServerID, src, log)
		cp.PreRan = false
		m.checkpoint(ctx, j, cp)
	}
	if err != nil {
		if errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			return nil, err // resumed on the next start
		}
		m.fail(ctx, b, err)
		return nil, err
	}
	var res engine.SnapshotResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	if res.Skipped > 0 {
		warning = joinWarning(warning, fmt.Sprintf("%d files or directories changed while being read and were skipped", res.Skipped))
	}
	if err := m.finish(ctx, b, res, warning); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(log, "done: %d files, %d bytes, %d bytes new\n", res.Files, res.Size, res.Uploaded)
	if warning != "" {
		_, _ = fmt.Fprintln(log, "warning:", warning)
	}
	if err := m.retention(ctx, b.ServerID, b.DestinationID, pol.Retention, log); err != nil {
		// The backup itself is fine; retention runs again after the next.
		_, _ = fmt.Fprintln(log, "retention failed:", err)
		m.log.Warn("backup retention failed", "server", b.ServerID, "err", err)
	}
	return map[string]any{"backup_id": b.ID, "size": res.Size, "files": res.Files, "uploaded": res.Uploaded}, nil
}

func (m *Manager) checkpoint(ctx context.Context, j jobs.Job, v any) {
	if err := m.o.Jobs.Checkpoint(ctx, j.ID, v); err != nil {
		m.log.Warn("saving backup progress failed", "job", j.ID, "err", err)
	}
}

func (m *Manager) running(serverID string) bool {
	st, err := m.o.Servers.Status(serverID)
	return err == nil && st.State == server.Running
}

// pre sends the egg's pre-backup commands and waits for its WaitFor line.
// It returns a warning if the game didn't confirm in time: the backup still
// runs, since a possibly inconsistent backup beats none.
func (m *Manager) pre(ctx context.Context, serverID string, src server.BackupSource, log io.Writer) string {
	st, err := m.o.Servers.Status(serverID)
	if err != nil {
		return ""
	}
	var lines <-chan string
	if src.WaitFor != "" {
		_, sub, unsub := st.Console.Subscribe()
		defer unsub()
		lines = sub.C
	}
	for _, c := range src.Pre {
		_, _ = fmt.Fprintf(log, "pre-backup command: %s\n", c)
		if err := m.o.Servers.SendCommand(serverID, hookUser, c); err != nil {
			return fmt.Sprintf("the pre-backup command %q failed: %v", c, err)
		}
	}
	if lines == nil {
		return ""
	}
	t := time.NewTimer(m.o.HookTimeout)
	defer t.Stop()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return "the server stopped before confirming the save"
			}
			if strings.Contains(l, src.WaitFor) {
				return ""
			}
		case <-t.C:
			return fmt.Sprintf("the server didn't confirm the save within %s (waited for %q)", m.o.HookTimeout, src.WaitFor)
		case <-ctx.Done():
			return ""
		}
	}
}

// post sends the egg's post-backup commands, if the server is still up.
func (m *Manager) post(serverID string, src server.BackupSource, log io.Writer) {
	if !m.running(serverID) {
		return
	}
	for _, c := range src.Post {
		_, _ = fmt.Fprintf(log, "post-backup command: %s\n", c)
		if err := m.o.Servers.SendCommand(serverID, hookUser, c); err != nil {
			_, _ = fmt.Fprintf(log, "  failed: %v\n", err)
		}
	}
}

func joinWarning(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func progressLog(log io.Writer) func(engine.Progress) {
	return func(p engine.Progress) {
		if p.Total > 0 {
			_, _ = fmt.Fprintf(log, "progress: %d of %d bytes\n", p.Bytes, p.Total)
		} else {
			_, _ = fmt.Fprintf(log, "progress: %d bytes\n", p.Bytes)
		}
	}
}

func (m *Manager) finish(ctx context.Context, b *Backup, res engine.SnapshotResult, warning string) error {
	now := m.o.Now()
	b.Status, b.Size, b.Files, b.Uploaded, b.Warning, b.Error, b.FinishedAt = StatusOK, res.Size, res.Files, res.Uploaded, warning, "", now
	b.snapshotID = res.SnapshotID
	err := m.o.Store.WriteTx(context.WithoutCancel(ctx), func(q *store.Queries) error {
		if err := q.FinishBackup(ctx, store.FinishBackupParams{
			SnapshotID: res.SnapshotID, Size: res.Size, Files: res.Files, Uploaded: res.Uploaded, Warning: warning,
			FinishedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: b.ID,
		}); err != nil {
			return err
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventFinished, ServerID: b.ServerID, Data: b.eventData()})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

func (m *Manager) fail(ctx context.Context, b *Backup, cause error) {
	ctx = context.WithoutCancel(ctx)
	now := m.o.Now()
	b.Status, b.Error, b.FinishedAt = StatusFailed, cause.Error(), now
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.FailBackup(ctx, store.FailBackupParams{Error: b.Error, FinishedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: b.ID}); err != nil {
			return err
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventFinished, ServerID: b.ServerID, Data: b.eventData()})
		return err
	})
	if err != nil {
		m.log.Error("recording a failed backup failed", "backup", b.ID, "err", err)
		return
	}
	m.o.Events.Wake()
}

// retention deletes the server's backups on dest that no rule keeps.
// Locked, safety, final, and unfinished backups are never counted or
// deleted.
func (m *Manager) retention(ctx context.Context, serverID, destID string, r Retention, log io.Writer) error {
	rows, err := m.o.Store.Read.ListDestinationBackups(ctx, store.ListDestinationBackupsParams{DestinationID: destID, ServerID: serverID})
	if err != nil {
		return err
	}
	var cands []candidate
	for _, row := range rows {
		if row.Status == StatusOK && row.Locked == 0 && row.Kind != KindSafety && row.Kind != KindFinal {
			cands = append(cands, candidate{ID: row.ID, At: time.UnixMilli(row.CreatedAt)})
		}
	}
	ids := r.expired(cands, m.o.Location)
	if len(ids) == 0 {
		return nil
	}
	_, _ = fmt.Fprintf(log, "retention: deleting %d old backups\n", len(ids))
	return m.deleteBackups(ctx, destID, ids, "retention", log)
}

// deleteBackups deletes backups' snapshots, then their rows. Snapshots
// already gone are fine, so it can be retried.
func (m *Manager) deleteBackups(ctx context.Context, destID string, ids []string, reason string, log io.Writer) error {
	var snaps []string
	var found []*Backup
	for _, id := range ids {
		b, err := m.row(ctx, id)
		if err != nil {
			continue // already deleted
		}
		found = append(found, b)
		if b.snapshotID != "" {
			snaps = append(snaps, b.snapshotID)
		}
	}
	if len(snaps) > 0 {
		if _, err := m.run(ctx, destID, request{Op: opDelete, Delete: snaps}, nil, log); err != nil {
			return err
		}
	}
	err := m.o.Store.WriteTx(context.WithoutCancel(ctx), func(q *store.Queries) error {
		for _, b := range found {
			if err := q.DeleteBackupRow(ctx, b.ID); err != nil {
				return err
			}
			if _, err := events.AppendTx(ctx, q, events.Event{Type: EventDeleted, ServerID: b.ServerID, Data: map[string]any{
				"backup_id": b.ID, "reason": reason,
			}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

func (m *Manager) deleteJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p deletePayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	if _, err := m.o.Store.Read.GetBackupDestination(ctx, p.DestinationID); err != nil {
		return nil, nil //nolint:nilnil,nilerr // the destination was deleted, and its backups with it
	}
	return nil, m.deleteBackups(ctx, p.DestinationID, p.BackupIDs, p.Reason, log)
}

func (m *Manager) maintainJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p maintainPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	if _, err := m.o.Store.Read.GetBackupDestination(ctx, p.DestinationID); err != nil {
		return nil, nil //nolint:nilnil,nilerr // deleted meanwhile
	}
	_, err := m.run(ctx, p.DestinationID, request{Op: opMaintain}, nil, log)
	return nil, err
}

// restoreJob restores a backup: it stops the server, takes a safety backup
// of the current files, replaces them with the backup, and starts the
// server again if it was meant to be running.
func (m *Manager) restoreJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p restorePayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	b, err := m.row(ctx, p.BackupID)
	if err != nil {
		return nil, err
	}
	src, err := m.o.Servers.BackupSource(ctx, j.ServerID)
	if err != nil {
		return nil, err
	}
	var cp restoreCheckpoint
	resumed, err := j.DecodeCheckpoint(&cp)
	if err != nil {
		return nil, err
	}
	if !resumed {
		cp.Start = src.DesiredRunning
		m.checkpoint(ctx, j, cp)
	}
	var res engine.RestoreResult
	err = m.o.Servers.Restore(ctx, j.ServerID, cp.Start, func(ctx context.Context, dir string) error {
		if !cp.Safety {
			id, err := m.safetyBackup(ctx, j, &cp, b, dir, log)
			if err != nil {
				return fmt.Errorf("safety backup: %w", err)
			}
			cp.SafetyID, cp.Safety = id, true
			m.checkpoint(ctx, j, cp)
		}
		_, _ = fmt.Fprintf(log, "restoring backup %s\n", b.ID)
		out, err := m.run(ctx, b.DestinationID, request{Op: opRestore, Restore: &engine.RestoreRequest{
			SnapshotID: b.snapshotID, Dir: dir, UID: src.UID, GID: src.GID, MaxSize: src.DiskLimit,
		}}, progressLog(log), log)
		if err != nil {
			return err
		}
		return json.Unmarshal(out, &res)
	})
	if errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
		return nil, err // resumed on the next start
	}
	data := map[string]any{"backup_id": b.ID, "job_id": j.ID, "user": p.User, "ok": err == nil}
	if cp.SafetyID != "" {
		data["safety_backup_id"] = cp.SafetyID
	}
	if err != nil {
		data["error"] = err.Error()
	}
	if _, aerr := m.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: EventRestored, ServerID: j.ServerID, Data: data}); aerr != nil {
		m.log.Error("recording a restore failed", "job", j.ID, "err", aerr)
	}
	m.o.Events.Wake()
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(log, "restored %d files, %d bytes\n", res.Files, res.Size)
	return map[string]any{"backup_id": b.ID, "safety_backup_id": cp.SafetyID, "files": res.Files, "size": res.Size}, nil
}

// JobBackup backs up a stopped server's files for a job that's about to
// replace or delete them: a wipe's safety backup (KindSafety) or a
// deletion's final backup (KindFinal). It runs inside that job rather than
// as a backup job of its own, since the job holds the server's lock and a
// separate job would wait for it forever. A resumed job gets the backup it
// already finished, and retries one that was interrupted. An empty directory
// needs none: "" and no error.
func (m *Manager) JobBackup(ctx context.Context, serverID, jobID, kind string, log io.Writer) (string, error) {
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
	src, err := m.o.Servers.BackupSource(ctx, serverID)
	if err != nil {
		return "", err
	}
	if empty, err := isEmpty(src.Dir); err != nil || empty {
		return "", err
	}
	pol, err := m.Policy(ctx, serverID)
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
			ID: id, ServerID: serverID, DestinationID: pol.DestinationID, Kind: kind,
			Status: StatusPending, JobID: jobID, CreatedBy: hookUser, CreatedAt: now,
		}
		switch {
		case kind == KindSafety:
			b.ExpiresAt = now.Add(SafetyTTL)
		case pol.DestinationID == LocalDestination:
			b.ExpiresAt = now.Add(FinalTTL)
		}
		if err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error { return m.insert(ctx, q, b) }); err != nil {
			return "", err
		}
	}
	if kind == KindFinal {
		_, _ = fmt.Fprintf(log, "taking a final backup to %s\n", b.DestinationID)
	} else {
		_, _ = fmt.Fprintf(log, "taking a safety backup to %s\n", b.DestinationID)
	}
	return b.ID, m.snapshotInto(ctx, b, src.Dir, pol.Ignore, log)
}

// snapshotInto takes the snapshot for a backup row made by another job,
// and records how it went.
func (m *Manager) snapshotInto(ctx context.Context, b *Backup, dir string, ignore []string, log io.Writer) error {
	if err := m.o.Store.Write.SetBackupRunning(ctx, b.ID); err != nil {
		return err
	}
	out, err := m.run(ctx, b.DestinationID, request{Op: opSnapshot, Snapshot: &engine.SnapshotRequest{
		ServerID: b.ServerID, BackupID: b.ID, Dir: dir, Ignore: ignore,
	}}, progressLog(log), log)
	if err != nil {
		if !errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			m.fail(ctx, b, err)
		}
		return err
	}
	var res engine.SnapshotResult
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	return m.finish(ctx, b, res, "")
}

// safetyBackup backs up the stopped server's current files before a
// restore replaces them. An empty directory needs none. The backup goes to
// the same destination as the one being restored.
func (m *Manager) safetyBackup(ctx context.Context, j jobs.Job, cp *restoreCheckpoint, restoring *Backup, dir string, log io.Writer) (string, error) {
	if empty, err := isEmpty(dir); err != nil || empty {
		return "", err
	}
	b := &Backup{
		ID: cp.SafetyID, ServerID: j.ServerID, DestinationID: restoring.DestinationID, Kind: KindSafety,
		Status: StatusPending, JobID: j.ID, CreatedBy: hookUser, CreatedAt: m.o.Now(), ExpiresAt: m.o.Now().Add(SafetyTTL),
	}
	if b.ID == "" {
		id, err := newID()
		if err != nil {
			return "", err
		}
		b.ID = id
		cp.SafetyID = id
		if err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error { return m.insert(ctx, q, b) }); err != nil {
			return "", err
		}
		m.checkpoint(ctx, j, *cp)
	} else if existing, err := m.row(ctx, b.ID); err == nil {
		b = existing
	}
	_, _ = fmt.Fprintln(log, "taking a safety backup of the current files")
	pol, err := m.Policy(ctx, j.ServerID)
	if err != nil {
		return "", err
	}
	return b.ID, m.snapshotInto(ctx, b, dir, pol.Ignore, log)
}
