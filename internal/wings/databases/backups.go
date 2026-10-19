package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// A server's backups hold its databases (docs/DATABASES.md#backups): before
// the snapshot, each one is dumped, as its own user, into a folder outside
// the server's files (so a dump never counts against its disk or fills
// it), which the snapshot adds at the top as DumpsFolder. Restoring the
// backup brings the folder back into the server's files; each dump is then
// run into its database, emptied first, and the folder removed.

// DumpsFolder holds a server's database dumps in its backups.
const DumpsFolder = ".raptor-databases"

// JobRestore restores one database from a backup.
const JobRestore = "db.restore"

// Backups is what restoring one database needs from the backups.
type Backups interface {
	// ExtractDatabase puts a backup's dump of one of the server's databases
	// in a new folder inside dir and returns the dump's path.
	// file is the dump's name in the backup's DumpsFolder.
	ExtractDatabase(ctx context.Context, serverID, backupID, name, file, dir string, log io.Writer) (string, error)
}

// SetBackups gives the service the backups, once there are any (they're
// made after it).
func (s *Service) SetBackups(b Backups) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backups = b
}

// dumpsDir is where dumps are made, on the server data volume beside the
// instances' folders, under no server's quota.
func (s *Service) dumpsDir() string { return filepath.Join(s.o.Dir, ".dumps") }

// Dump dumps each of a server's databases into a new folder for a backup,
// and returns the folder and the databases in it; "" when the server has
// none. One that can't be dumped (its instance is stopped) is left out,
// with a warning saying so: the backup of the files still goes ahead.
func (s *Service) Dump(ctx context.Context, serverID string, log io.Writer) (string, []string, string, error) {
	rows, err := s.o.Store.Read.ListServerDatabases(ctx, serverID)
	if err != nil || len(rows) == 0 {
		return "", nil, "", err
	}
	if err := os.MkdirAll(s.dumpsDir(), 0o700); err != nil {
		return "", nil, "", err
	}
	dir, err := os.MkdirTemp(s.dumpsDir(), "backup-")
	if err != nil {
		return "", nil, "", err
	}
	var names, failed []string
	for _, r := range rows {
		_, _ = fmt.Fprintf(log, "dumping the database %s\n", r.Label)
		in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
		if err == nil {
			err = s.dumpFile(ctx, r, filepath.Join(dir, r.Label+dumpExt(in.Engine)), log)
		}
		if err != nil {
			_, _ = fmt.Fprintf(log, "  failed: %v\n", err)
			failed = append(failed, fmt.Sprintf("%s (%v)", r.Label, err))
			continue
		}
		names = append(names, r.Label)
	}
	warning := ""
	if len(failed) > 0 {
		warning = "these databases aren't in it: " + strings.Join(failed, "; ")
	}
	if len(names) == 0 {
		_ = os.RemoveAll(dir)
		return "", nil, warning, nil
	}
	return dir, names, warning, nil
}

func (s *Service) dumpFile(ctx context.Context, r store.Database, path string, log io.Writer) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // in a folder Wings just made
	if err != nil {
		return err
	}
	err = s.dumpTo(ctx, r, "", "", f, log)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// Restore runs the dumps a restored backup put in a server's directory
// into its databases, then removes them. A database the server doesn't
// have any more keeps its dump there, for importing by hand. It returns a
// warning naming any that weren't restored. The folder came from the
// server's files, so nothing in it is followed out of it.
func (s *Service) Restore(ctx context.Context, serverID, dir string, log io.Writer) (string, error) {
	sroot, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = sroot.Close() }()
	if fi, err := sroot.Lstat(DumpsFolder); errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	froot, err := sroot.OpenRoot(DumpsFolder)
	if err != nil {
		return "", err
	}
	defer func() { _ = froot.Close() }()
	d, err := froot.Open(".")
	if err != nil {
		return "", err
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return "", err
	}
	rows, err := s.o.Store.Read.ListServerDatabases(ctx, serverID)
	if err != nil {
		return "", err
	}
	var problems []string
	kept := false
	for _, e := range entries {
		label, ok := dumpLabel(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		i := slices.IndexFunc(rows, func(r store.Database) bool { return r.Label == label })
		if i < 0 {
			kept = true
			problems = append(problems, fmt.Sprintf("%s isn't one of the server's databases any more; its dump is in /%s", label, DumpsFolder))
			continue
		}
		_, _ = fmt.Fprintf(log, "restoring the database %s\n", label)
		err := func() error {
			f, err := froot.OpenFile(e.Name(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
				return errors.New("its dump isn't a regular file")
			}
			return s.replace(ctx, rows[i], f, log)
		}()
		if err != nil {
			_, _ = fmt.Fprintf(log, "  failed: %v\n", err)
			kept = true
			problems = append(problems, fmt.Sprintf("%s wasn't restored (%v); its dump is in /%s", label, err, DumpsFolder))
			continue
		}
		_ = froot.Remove(e.Name())
	}
	if !kept {
		_ = sroot.RemoveAll(DumpsFolder)
	}
	return strings.Join(problems, "; "), nil
}

// replace empties a database, keeping its user and read-only user, and
// runs a dump into it.
func (s *Service) replace(ctx context.Context, r store.Database, src io.Reader, log io.Writer) error {
	if err := s.busy(r.InstanceID); err != nil {
		return err
	}
	unlock := s.lock(r.InstanceID)
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		unlock()
		return err
	}
	if s.state(r.InstanceID) != StateRunning {
		unlock()
		return notReady{fmt.Sprintf("%s isn't running", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		unlock()
		return err
	}
	if in.Engine == Redis {
		// Its data is the whole instance's: Redis loads a snapshot only
		// when it starts.
		defer unlock()
		return s.restoreRedis(ctx, in, src, log)
	}
	err = s.recreate(ctx, in, r, root)
	unlock()
	if err != nil {
		return fmt.Errorf("emptying it: %w", err)
	}
	return s.importFrom(ctx, in, r, src, log)
}

// recreate drops a database and makes it again, empty, for its user, with
// its read-only user's rights back.
func (s *Service) recreate(ctx context.Context, in Instance, r store.Database, root string) error {
	if err := checkNames(r.DbName); err != nil {
		return err
	}
	if in.Engine == MongoDB {
		// Its users are stored in it, and stay.
		return mongoEmpty(ctx, s.dir(in.ID), root, r.DbName)
	}
	db, err := connect(in, s.dir(in.ID), root)
	if err != nil {
		return err
	}
	drop := "DROP DATABASE IF EXISTS `" + r.DbName + "`"
	if in.Engine == Postgres {
		drop = `DROP DATABASE IF EXISTS "` + r.DbName + `" WITH (FORCE)`
	}
	err = exec(ctx, db, drop)
	_ = db.Close()
	if err != nil {
		return err
	}
	return s.provision(ctx, in, r, root)
}

// provision makes a database, its user, and its read-only user, as they
// are in state.db. It can run again.
func (s *Service) provision(ctx context.Context, in Instance, r store.Database, root string) error {
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return err
	}
	dir := s.dir(in.ID)
	if err := createDatabase(ctx, in, dir, root, r.DbName, r.Username, pass); err != nil {
		return err
	}
	if r.RoUsername == "" || r.RoPassword == nil {
		return nil
	}
	ro, err := s.sealer.open(r.RoPassword)
	if err != nil {
		return err
	}
	return grantReadOnly(ctx, in, dir, root, r.DbName, r.Username, r.RoUsername, ro)
}

// RestoreJob is db.restore's payload.
type RestoreJob struct {
	Database string `json:"database"`
	BackupID string `json:"backup_id"`
	// Before is the export of the database taken first, in the server's
	// files.
	Before string `json:"before"`
}

// RestoreDatabase queues restoring one of a server's databases from one of
// its backups. What the database has now is exported into the server's
// database-exports folder first; it returns the job and that file.
func (s *Service) RestoreDatabase(ctx context.Context, serverID, id, backupID string) (string, string, error) {
	if s.o.Jobs == nil {
		return "", "", errors.New("restoring a database isn't available")
	}
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", "", err
	}
	if backupID == "" {
		return "", "", invalid{"pick a backup"}
	}
	before := filepath.ToSlash(filepath.Join(ExportDir, r.Label+"-before-restore-"+time.Now().UTC().Format("20060102-150405")+".sql.gz"))
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobRestore, ServerID: serverID, Payload: RestoreJob{
		Database: id, BackupID: backupID, Before: before,
	}})
	return jobID, before, err
}

func (s *Service) restoreJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p RestoreJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	pr := s.transfers.start(j.ID)
	defer s.transfers.done(j.ID)
	r, err := s.database(ctx, j.ServerID, p.Database)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	bk := s.backups
	s.mu.Unlock()
	if bk == nil {
		return nil, errors.New("the backups aren't ready yet")
	}
	if err := os.MkdirAll(s.dumpsDir(), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(s.dumpsDir(), "restore-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	_, _ = fmt.Fprintf(log, "taking the dump of %s out of backup %s\n", r.Label, p.BackupID)
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return nil, err
	}
	path, err := bk.ExtractDatabase(ctx, j.ServerID, p.BackupID, r.Label, r.Label+dumpExt(in.Engine), tmp, log)
	if err != nil {
		return nil, err
	}
	// What it has now, so the restore can be undone by importing it.
	_, _ = fmt.Fprintf(log, "exporting what it has now to /%s\n", p.Before)
	if err := s.exportTo(ctx, j.ServerID, ExportJob{Database: p.Database, Format: "sql", Path: p.Before}, &pr.bytes, log); err != nil {
		return nil, fmt.Errorf("exporting it before the restore: %w", err)
	}
	pr.bytes.Store(0)
	f, err := os.Open(path) //nolint:gosec // in a folder Wings just made
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil {
		pr.total.Store(st.Size())
	}
	_, _ = fmt.Fprintf(log, "restoring %s\n", r.Label)
	if err := s.replace(ctx, r, counter{r: f, n: &pr.bytes}, log); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintln(log, "done")
	return map[string]any{"backup_id": p.BackupID, "before": p.Before}, nil
}

// dumpExt is the extension of an engine's dumps in a backup: SQL, a
// mongodump archive, or Redis' snapshot.
func dumpExt(engine string) string {
	switch engine {
	case MongoDB:
		return ".archive"
	case Redis:
		return ".rdb"
	}
	return ".sql"
}

// dumpLabel is the database a dump in a backup is of, by its file name.
func dumpLabel(file string) (string, bool) {
	for _, ext := range []string{".sql", ".archive", ".rdb"} {
		if label, ok := strings.CutSuffix(file, ext); ok && label != "" {
			return label, true
		}
	}
	return "", false
}

// restoreRedis replaces a Redis instance's data with a snapshot: it's
// stopped, the snapshot put in its data folder (its append-only log,
// which would win over it, removed), and started again. Its users stay.
func (s *Service) restoreRedis(ctx context.Context, in Instance, src io.Reader, log io.Writer) error {
	dir := s.dir(in.ID)
	_, _ = fmt.Fprintln(log, "stopping it to load the snapshot")
	if err := s.o.Runtime.StopService(ctx, ContainerName(in.ID), 2*time.Minute); err != nil {
		return err
	}
	tmp := filepath.Join(dataDir(dir), "dump.rdb.restore")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // Wings' own folder
	if err == nil {
		_, err = io.Copy(f, src)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = os.Chown(tmp, engineUID, engineUID)
	}
	// With its append-only log on, Redis loads only that (since 7.0,
	// a manifest of a base file, which may be a snapshot, and increments
	// after it): the snapshot becomes a new log's base.
	aof := filepath.Join(dataDir(dir), "appendonlydir")
	if err == nil {
		err = os.RemoveAll(aof)
	}
	if err == nil {
		err = os.Mkdir(aof, 0o700)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(aof, "appendonly.aof.1.base.rdb"))
	}
	for _, f := range [][2]string{
		{"appendonly.aof.1.incr.aof", ""},
		{"appendonly.aof.manifest", "file appendonly.aof.1.base.rdb seq 1 type b\nfile appendonly.aof.1.incr.aof seq 1 type i\n"},
	} {
		if err == nil {
			err = os.WriteFile(filepath.Join(aof, f[0]), []byte(f[1]), 0o600)
		}
	}
	for _, p := range []string{aof, filepath.Join(aof, "appendonly.aof.1.base.rdb"), filepath.Join(aof, "appendonly.aof.1.incr.aof"), filepath.Join(aof, "appendonly.aof.manifest")} {
		if err == nil {
			if cerr := os.Lchown(p, engineUID, engineUID); cerr != nil && !errors.Is(cerr, fs.ErrPermission) {
				err = cerr
			}
		}
	}
	if err == nil {
		_ = os.Remove(filepath.Join(dataDir(dir), "dump.rdb"))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	// Started again either way.
	if serr := s.ensureLocked(context.WithoutCancel(ctx), in.ID); serr != nil {
		s.setStatus(in.ID, StateFailed, serr)
		return errors.Join(err, serr)
	}
	s.setStatus(in.ID, StateRunning, nil)
	return err
}
