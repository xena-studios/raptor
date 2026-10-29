package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// An instance's own backups (docs/DATABASES.md#backups, DECISIONS #343):
// the backups take them (internal/wings/backup); this is the databases'
// side. A backup holds a dump of every database on the instance, named
// after the database (db_name, unique on it), taken as each database's own
// user like a server backup's dumps.

// Jobs for instance backups.
const (
	// JobInstanceRestore restores one database from an instance backup,
	// after a safety backup of the instance.
	JobInstanceRestore = "db.instance.restore"
	// JobInstanceDelete deletes an instance after its final backup.
	JobInstanceDelete = "db.instance.delete"
)

// EventDeleteFailed: an instance wasn't deleted because its final backup
// failed.
const EventDeleteFailed = "db.instance.delete.failed"

// InstanceBackups is what an instance's own backups need from the backups.
type InstanceBackups interface {
	// InstanceJobBackup backs up an instance inside the caller's job: a
	// safety backup before a restore, or a final one before a deletion.
	InstanceJobBackup(ctx context.Context, instanceID, jobID, kind string, log io.Writer) (string, error)
	// ExtractInstanceDatabase puts an instance backup's dump of one
	// database in a new folder inside dir and returns the dump's path.
	ExtractInstanceDatabase(ctx context.Context, instanceID, backupID, name, file, dir string, log io.Writer) (string, error)
	// InstanceDeleted forgets a deleted instance's local backups but its
	// final one.
	InstanceDeleted(ctx context.Context, instanceID string)
}

func (s *Service) instanceBackups() InstanceBackups {
	s.mu.Lock()
	defer s.mu.Unlock()
	ib, _ := s.backups.(InstanceBackups)
	return ib
}

// InstanceIDs lists the node's instances, for their backups.
func (s *Service) InstanceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, nil
}

// InstanceName returns an instance's name and engine, for its backups'
// events; "" for one that's gone.
func (s *Service) InstanceName(ctx context.Context, id string) (string, string) {
	in, _, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return "", ""
	}
	return in.Name, in.Engine
}

// DumpInstance dumps every database on an instance into a new folder for a
// backup. Any that can't be dumped (the instance is stopped) fails the
// backup: an instance's backup that quietly leaves databases out isn't one.
func (s *Service) DumpInstance(ctx context.Context, id string, log io.Writer) (string, []string, error) {
	in, _, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return "", nil, err
	}
	rows, err := s.instanceDatabases(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(s.dumpsDir(), 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(s.dumpsDir(), "instance-")
	if err != nil {
		return "", nil, err
	}
	var names []string
	for _, r := range rows {
		_, _ = fmt.Fprintf(log, "dumping the database %s\n", r.DbName)
		if err := s.dumpFile(ctx, r, filepath.Join(dir, r.DbName+dumpExt(in.Engine)), log); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("dumping %s: %w", r.DbName, err)
		}
		names = append(names, r.DbName)
	}
	return dir, names, nil
}

// instanceRestoreJob is db.instance.restore's payload.
type instanceRestoreJob struct {
	Instance string `json:"instance_id"`
	Database string `json:"database"`
	BackupID string `json:"backup_id"`
}

// RestoreFromInstanceBackup queues restoring one database on an instance
// from one of the instance's backups. A safety backup of the instance is
// taken first, so the restore can be undone the same way.
func (s *Service) RestoreFromInstanceBackup(ctx context.Context, instanceID, databaseID, backupID string) (string, error) {
	if s.o.Jobs == nil || s.instanceBackups() == nil {
		return "", errors.New("restoring from an instance's backups isn't available")
	}
	r, err := s.o.Store.Read.GetDatabase(ctx, databaseID)
	if err != nil || r.InstanceID != instanceID {
		return "", fmt.Errorf("%w: %s", ErrNotFound, databaseID)
	}
	if backupID == "" {
		return "", invalid{"pick a backup"}
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobInstanceRestore, Payload: instanceRestoreJob{
		Instance: instanceID, Database: databaseID, BackupID: backupID,
	}})
}

func (s *Service) instanceRestoreJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p instanceRestoreJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	ib := s.instanceBackups()
	if ib == nil {
		return nil, errors.New("the backups aren't ready yet")
	}
	r, err := s.o.Store.Read.GetDatabase(ctx, p.Database)
	if err != nil || r.InstanceID != p.Instance {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, p.Database)
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, p.Instance)
	if err != nil {
		return nil, err
	}
	pr := s.transfers.start(j.ID)
	defer s.transfers.done(j.ID)
	// What the instance has now, so the restore can be undone by restoring
	// this backup.
	safety, err := ib.InstanceJobBackup(ctx, p.Instance, j.ID, "safety", log)
	if err != nil {
		return nil, fmt.Errorf("safety backup: %w", err)
	}
	if err := os.MkdirAll(s.dumpsDir(), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(s.dumpsDir(), "restore-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	_, _ = fmt.Fprintf(log, "taking the dump of %s out of backup %s\n", r.DbName, p.BackupID)
	path, err := ib.ExtractInstanceDatabase(ctx, p.Instance, p.BackupID, r.DbName, r.DbName+dumpExt(in.Engine), tmp, log)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // in a folder Wings just made
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil {
		pr.total.Store(st.Size())
	}
	_, _ = fmt.Fprintf(log, "restoring %s\n", r.DbName)
	if err := s.replace(ctx, r, counter{r: f, n: &pr.bytes}, log); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintln(log, "done")
	return map[string]any{"backup_id": p.BackupID, "safety_backup_id": safety}, nil
}

// instanceDeleteJob is db.instance.delete's payload.
type instanceDeleteJob struct {
	Instance string `json:"instance_id"`
}

// DeleteInstance deletes an instance, after a final backup of its
// databases when it has any and the backups are there: then it's a job,
// whose ID it returns, and the instance stays if the backup fails.
// skipBackup deletes it at once.
func (s *Service) DeleteInstance(ctx context.Context, id string, skipBackup bool) (string, error) {
	if _, _, err := s.instance(ctx, s.o.Store.Read, id); err != nil {
		return "", err
	}
	rows, err := s.instanceDatabases(ctx, id)
	if err != nil {
		return "", err
	}
	if skipBackup || len(rows) == 0 || s.o.Jobs == nil || s.instanceBackups() == nil {
		if err := s.Delete(ctx, id); err != nil {
			return "", err
		}
		if ib := s.instanceBackups(); ib != nil {
			ib.InstanceDeleted(ctx, id)
		}
		return "", nil
	}
	if err := s.busy(id); err != nil {
		return "", err
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobInstanceDelete, Payload: instanceDeleteJob{Instance: id}})
}

func (s *Service) instanceDeleteJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p instanceDeleteJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, p.Instance)
	if errors.Is(err, ErrNotFound) {
		return nil, nil //nolint:nilnil // deleted meanwhile
	}
	if err != nil {
		return nil, err
	}
	ib := s.instanceBackups()
	if ib == nil {
		return nil, errors.New("the backups aren't ready yet")
	}
	final, err := ib.InstanceJobBackup(ctx, p.Instance, j.ID, "final", log)
	if err != nil {
		if !errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
			s.event(ctx, EventDeleteFailed, in, map[string]any{"error": "its final backup failed: " + err.Error()})
		}
		return nil, fmt.Errorf("the instance wasn't deleted: its final backup failed: %w", err)
	}
	_, _ = fmt.Fprintln(log, "deleting the instance")
	if err := s.Delete(ctx, p.Instance); err != nil {
		return nil, err
	}
	ib.InstanceDeleted(ctx, p.Instance)
	return map[string]any{"final_backup_id": final}, nil
}
