package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// Deleting is the state of a server whose final backup is being taken
// before it's deleted. It's stopped, and power actions are refused.
const Deleting State = "deleting"

// ErrDeleting is returned for a server that's being deleted.
var ErrDeleting = errors.New("server is being deleted")

// Kinds of the backups a job takes for itself through Options.JobBackup
// (the backup package's KindSafety and KindFinal).
const (
	BackupSafety = "safety"
	BackupFinal  = "final"
)

// JobDelete is the job that takes a final backup and then deletes a server.
const JobDelete = "server.delete"

// Events of deletion and wiping.
const (
	EventDeleteQueued = "server.delete.queued"
	EventDeleteFailed = "server.delete.failed" // the final backup failed; the server was kept
	EventWiped        = "server.wiped"         // files removed for a wipe and reinstall
)

type deletePayload struct {
	User string `json:"user,omitempty"`
}

// DeleteWithBackup queues a job that stops the server, takes a final backup
// to its backup destination, and only then deletes it. If the backup fails,
// the server is kept (stopped). It returns the job's ID.
func (m *Manager) DeleteWithBackup(ctx context.Context, id, user string) (string, error) {
	if m.o.JobBackup == nil {
		return "", errors.New("backups aren't available on this node")
	}
	if _, err := m.instance(id); err != nil {
		return "", err
	}
	// Installs and backups waiting for the server would only delay the
	// deletion (and an install would change the files being backed up).
	if err := m.cancelJobs(ctx, id, ""); err != nil {
		return "", err
	}
	job, err := m.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobDelete, ServerID: id, Payload: deletePayload{User: user}})
	if err != nil {
		return "", err
	}
	m.publish(EventDeleteQueued, id, 0, map[string]any{"job_id": job, "user": user, "final_backup": true})
	return job, nil
}

func (m *Manager) deleteJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	i, err := m.instance(j.ServerID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil //nolint:nilnil // deleted before a Wings restart; nothing left to do
	}
	if err != nil {
		return nil, err
	}
	backupID, err := m.finalBackup(ctx, i, j.ID, log)
	if errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
		return nil, err // resumed on the next start
	}
	if err != nil {
		m.publish(EventDeleteFailed, j.ServerID, 0, map[string]any{"job_id": j.ID, "error": err.Error()})
		return nil, fmt.Errorf("final backup: %w; the server wasn't deleted", err)
	}
	_, _ = fmt.Fprintln(log, "deleting the server")
	data := map[string]any{"final_backup_id": backupID}
	if err := m.deleteNow(ctx, j.ServerID, j.ID, data); err != nil {
		return nil, err
	}
	return data, nil
}

// finalBackup stops the server and backs it up, in the Deleting state.
func (m *Manager) finalBackup(ctx context.Context, i *instance, jobID string, log io.Writer) (string, error) {
	i.power.Lock()
	defer i.power.Unlock()
	if err := i.stopLocked(ctx, false, true); err != nil {
		m.log.Warn("stop before delete failed; killing", "server", i.id, "err", err)
		if err := i.stopLocked(ctx, true, true); err != nil {
			return "", err
		}
	}
	i.setState(Deleting)
	i.console.Notice("taking a final backup before deleting the server")
	id, err := m.o.JobBackup(ctx, i.id, jobID, BackupFinal, log)
	if err != nil {
		// Still Deleting if Wings is stopping: the job resumes.
		if !errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			i.setState(Offline)
			i.console.Notice("final backup failed, so the server wasn't deleted: %v", err)
		}
		return "", err
	}
	return id, nil
}

// wipe takes a safety backup of the server's files and then removes them
// all, for a reinstall from scratch. Nothing is removed unless the backup
// succeeded (an empty directory needs none). Links are removed, never
// followed.
func (m *Manager) wipe(ctx context.Context, i *instance, jobID, dir string, log io.Writer) error {
	if m.o.JobBackup == nil {
		return errors.New("backups aren't available on this node")
	}
	i.console.Notice("taking a safety backup before wiping the files")
	backupID, err := m.o.JobBackup(ctx, i.id, jobID, BackupSafety, log)
	if err != nil {
		return fmt.Errorf("safety backup: %w; no files were removed", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := root.RemoveAll(name); err != nil {
			return fmt.Errorf("wipe: %w", err)
		}
	}
	_, _ = fmt.Fprintf(log, "wiped %d entries (safety backup %s)\n", len(names), backupID)
	i.console.Notice("files wiped")
	data := map[string]any{"job_id": jobID}
	if backupID != "" {
		data["safety_backup_id"] = backupID
	}
	m.publish(EventWiped, i.id, 0, data)
	return nil
}
