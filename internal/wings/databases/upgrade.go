package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Changing an instance's version (docs/DATABASES.md#changing-an-instances-version):
//
//   - In place (a newer patch, a newer MariaDB, MySQL 8.0 to 8.4): the data
//     is copied with the instance stopped, the new image started on it, and
//     MariaDB's system tables upgraded.
//   - A new PostgreSQL major, whose data files don't carry over: every
//     database is dumped as the superuser, the old data put aside, the new
//     version started on an empty folder, and the dumps restored into
//     databases made as they were.
//
// Either way every table's row count is compared before and after; if the
// new version doesn't start, or anything differs, the old data and version
// are put back. The data from before is kept GoBackFor, inside the
// instance's folder (so under its disk limit: a rename across XFS quota
// projects fails), and going back to it is one job.

// Jobs.
const (
	JobVersion = "db.instance.version"
	JobGoBack  = "db.instance.goback"
)

// GoBackFor is how long the data from before a version change is kept.
const GoBackFor = 7 * 24 * time.Hour

// Change is an instance's version change, going on or the last one.
type Change struct {
	JobID string `json:"job_id"`
	Kind  string `json:"kind"` // version, patch, or back
	From  string `json:"from"`
	To    string `json:"to"`
	// Step is what it's doing now.
	Step string `json:"step,omitempty"`
	Done bool   `json:"done,omitempty"`
	// Error is why it failed; it was put back as it was.
	Error string `json:"error,omitempty"`
}

// VersionJob is db.instance.version's payload, and db.instance.goback's
// (Instance alone).
type VersionJob struct {
	Instance string `json:"instance"`
	Version  string `json:"version,omitempty"`
	Patch    bool   `json:"patch,omitempty"`
}

// previousDir holds the data from before a change: previous/data, and
// while a change is going on, its marker and PostgreSQL's dumps.
func previousDir(dir string) string { return filepath.Join(dir, "previous") }

// marker records a change going on, with the instance's row from before
// it, so one interrupted by a Wings stop is put back at the next start.
type marker struct {
	// Switched: the instance's data is the new version's; before that,
	// nothing was changed but the instance stopped.
	Switched      bool   `json:"switched"`
	Version       string `json:"version"`
	Image         string `json:"image"`
	PrevVersion   string `json:"prev_version"`
	PrevImage     string `json:"prev_image"`
	RollbackUntil int64  `json:"rollback_until"`
}

func markerPath(dir string) string { return filepath.Join(previousDir(dir), ".changing") }

func writeMarker(dir string, m marker) error {
	return os.WriteFile(markerPath(dir), []byte(mustJSON(m)), 0o600)
}

// parseVersion splits a version line into numbers: 10.11 is [10 11].
func parseVersion(v string) []int {
	var out []int
	for p := range strings.SplitSeq(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// migrates decides how an instance goes from one version to another: in
// place, or (a new PostgreSQL major) by dump and restore. Older versions
// can't read newer data files, so going down isn't offered.
func migrates(engine, from, to string) (bool, error) {
	if c := slices.Compare(parseVersion(from), parseVersion(to)); c > 0 {
		return false, invalid{fmt.Sprintf("%s %s can't read the data of %s, so an instance can't go down to it. Within %d days of a change, Go back puts the version and data from before it back",
			engineName(engine), to, from, int(GoBackFor.Hours()/24))}
	}
	// MongoDB moves one major at a time, each finishing (its feature
	// compatibility version raised) before the next.
	if engine == MongoDB && parseVersion(to)[0]-parseVersion(from)[0] > 1 {
		return false, invalid{fmt.Sprintf("MongoDB goes up one major version at a time: change to %d.0 first", parseVersion(from)[0]+1)}
	}
	return engine == Postgres && parseVersion(from)[0] != parseVersion(to)[0], nil
}

// latest is the image of an instance's version line in this Wings.
func latest(engine, version string) string {
	v, err := lookup(engine, version)
	if err != nil || version == "" {
		return ""
	}
	return v.Image
}

func (s *Service) setChange(id string, c *Change) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == nil {
		delete(s.changes, id)
		return
	}
	cp := *c
	s.changes[id] = &cp
}

func (s *Service) step(id, step string, log io.Writer) {
	s.mu.Lock()
	if c := s.changes[id]; c != nil {
		c.Step = step
	}
	s.mu.Unlock()
	_, _ = fmt.Fprintln(log, step)
}

// busy is an error while an instance's version change is queued or going
// on: it holds the instance's lock for minutes.
func (s *Service) busy(id string) error {
	s.mu.Lock()
	c := s.changes[id]
	s.mu.Unlock()
	if c != nil && !c.Done {
		return notReady{"the instance is changing version; try again once it's done"}
	}
	return nil
}

// ChangeVersion queues moving an instance to another version, or to the
// newest patch of its own ("" or its version). It has to be running: its
// databases are counted before and after.
func (s *Service) ChangeVersion(ctx context.Context, id, version string) (string, error) {
	if s.o.Jobs == nil {
		return "", errors.New("version changes aren't available")
	}
	if err := s.busy(id); err != nil {
		return "", err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return "", err
	}
	if version == "" {
		version = in.Version
	}
	target, err := lookupOn(in.Engine, version, s.kernel())
	if err != nil {
		return "", err
	}
	if target.Version == in.Version && target.Image == in.Image {
		return "", invalid{fmt.Sprintf("it's already on the newest %s %s", engineName(in.Engine), in.Version)}
	}
	if _, err := migrates(in.Engine, in.Version, target.Version); err != nil {
		return "", err
	}
	if s.state(id) != StateRunning {
		return "", notReady{fmt.Sprintf("%s isn't running; start it first, so its databases can be checked before and after", in.Name)}
	}
	return s.enqueueChange(ctx, in, target.Version, target.Version == in.Version)
}

func (s *Service) enqueueChange(ctx context.Context, in Instance, version string, patch bool) (string, error) {
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobVersion, Payload: VersionJob{Instance: in.ID, Version: version, Patch: patch}})
	if err != nil {
		return "", err
	}
	kind := "version"
	if patch {
		kind = "patch"
	}
	s.setChange(in.ID, &Change{JobID: jobID, Kind: kind, From: in.Version, To: version, Step: "Waiting to start"})
	return jobID, nil
}

// GoBack queues putting back the version and data an instance had before
// its last version change. What changed in its databases since is lost.
func (s *Service) GoBack(ctx context.Context, id string) (string, error) {
	if s.o.Jobs == nil {
		return "", errors.New("version changes aren't available")
	}
	if err := s.busy(id); err != nil {
		return "", err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return "", err
	}
	if in.Previous == "" {
		return "", invalid{"there's no earlier version to go back to"}
	}
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobGoBack, Payload: VersionJob{Instance: id}})
	if err != nil {
		return "", err
	}
	s.setChange(id, &Change{JobID: jobID, Kind: "back", From: in.Version, To: in.Previous, Step: "Waiting to start"})
	return jobID, nil
}

func (s *Service) versionJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p VersionJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	defer s.lock(p.Instance)()
	in, r, err := s.instance(ctx, s.o.Store.Read, p.Instance)
	if err != nil {
		return nil, err
	}
	kind := "version"
	if p.Patch {
		kind = "patch"
	}
	s.setChange(in.ID, &Change{JobID: j.ID, Kind: kind, From: in.Version, To: p.Version})
	err = s.changeVersion(ctx, in, r, p, log)
	s.finishChange(ctx, in, err)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": p.Version}, nil
}

// finishChange records a version change's end, and raises
// db.instance.version for alerts.
func (s *Service) finishChange(ctx context.Context, in Instance, err error) {
	s.mu.Lock()
	c := s.changes[in.ID]
	var done Change
	if c != nil {
		c.Step, c.Done = "", true
		if err != nil {
			c.Error = err.Error()
		}
		done = *c
	}
	s.mu.Unlock()
	if c == nil {
		return
	}
	if cur, _, err := s.instance(ctx, s.o.Store.Read, in.ID); err == nil {
		in = cur // its version now
	}
	data := map[string]any{"kind": done.Kind, "from": done.From, "to": done.To, "ok": err == nil}
	if err != nil {
		data["error"] = err.Error()
	}
	s.event(ctx, EventVersion, in, data)
}

func (s *Service) changeVersion(ctx context.Context, in Instance, r store.DatabaseInstance, p VersionJob, log io.Writer) error {
	target, err := lookup(in.Engine, p.Version)
	if err != nil {
		return err
	}
	if target.Version == in.Version && target.Image == in.Image {
		_, _ = fmt.Fprintln(log, "it's already on", target.Image)
		return nil
	}
	migrate, err := migrates(in.Engine, in.Version, target.Version)
	if err != nil {
		return err
	}
	if s.state(in.ID) != StateRunning {
		return notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	root, err := s.sealer.open(r.RootPassword)
	if err != nil {
		return err
	}
	s.setStatus(in.ID, StateUpgrading, nil)
	defer func() {
		// However it ended, it's not changing any more.
		if s.state(in.ID) == StateUpgrading || s.state(in.ID) == StateStarting {
			s.setStatus(in.ID, StateRunning, nil)
		}
	}()
	dir := s.dir(in.ID)
	dbs, err := s.instanceDatabases(ctx, in.ID)
	if err != nil {
		return err
	}

	s.step(in.ID, "Counting the rows in every database", log)
	before, err := inventory(ctx, in, dir, root, dbs)
	if err != nil {
		return fmt.Errorf("counting its rows first: %w", err)
	}
	size, err := dirSize(dataDir(dir))
	if err != nil {
		return err
	}
	if s.o.FreeSpace != nil {
		if free, err := s.o.FreeSpace(dir); err == nil && free < size+512<<20 {
			return fmt.Errorf("the node has %d MiB free, and the copy of the data kept to go back to needs about %d MiB", free>>20, (size+512<<20)>>20)
		}
	}
	prev := previousDir(dir)
	if err := os.RemoveAll(prev); err != nil {
		return err
	}
	if err := os.MkdirAll(prev, 0o700); err != nil {
		return err
	}
	m := marker{Version: r.Version, Image: r.Image, PrevVersion: r.PrevVersion, PrevImage: r.PrevImage, RollbackUntil: r.RollbackUntil}
	if err := writeMarker(dir, m); err != nil {
		return err
	}
	stopped, switched := false, false
	undo := func(cause error) error {
		s.step(in.ID, "Putting it back as it was", log)
		if err := s.putBack(ctx, in.ID, m, stopped, switched); err != nil {
			s.setStatus(in.ID, StateFailed, err)
			return fmt.Errorf("%w; putting it back failed too: %v", cause, err) //nolint:errorlint // one error for people
		}
		s.setStatus(in.ID, StateRunning, nil)
		s.changed(ctx)
		return fmt.Errorf("it's back on %s %s as it was: %w", engineName(in.Engine), r.Version, cause)
	}

	if migrate {
		s.step(in.ID, "Dumping every database", log)
		if err := os.MkdirAll(filepath.Join(prev, "dumps"), 0o700); err != nil {
			return undo(err)
		}
		for _, d := range dbs {
			if err := s.superDump(ctx, in, root, d.DbName, filepath.Join(prev, "dumps", d.DbName+".sql"), log); err != nil {
				return undo(fmt.Errorf("dumping %s: %w", d.DbName, err))
			}
		}
	}
	s.step(in.ID, "Stopping it", log)
	stopped = true
	if err := s.o.Runtime.StopService(ctx, ContainerName(in.ID), 2*time.Minute); err != nil {
		return undo(err)
	}
	if migrate {
		if err := os.Rename(dataDir(dir), filepath.Join(prev, "data")); err != nil {
			return undo(err)
		}
	} else {
		s.step(in.ID, "Copying its data, to keep", log)
		if err := copyTree(dataDir(dir), filepath.Join(prev, "data")); err != nil {
			return undo(fmt.Errorf("copying its data: %w", err))
		}
	}
	m.Switched = true
	if err := writeMarker(dir, m); err != nil {
		return undo(err)
	}
	switched = true
	if err := s.o.Store.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{
		Version: target.Version, Image: target.Image, PrevVersion: r.Version, PrevImage: r.Image, UpdatedAt: time.Now().Unix(), ID: in.ID,
	}); err != nil {
		return undo(err)
	}

	s.step(in.ID, "Starting "+engineName(in.Engine)+" "+target.Version, log)
	if err := s.ensureLocked(ctx, in.ID); err != nil {
		return undo(err)
	}
	s.setStatus(in.ID, StateUpgrading, nil)
	in.Version, in.Image = target.Version, target.Image
	if in.Engine == MongoDB && !p.Patch {
		s.step(in.ID, "Raising its feature compatibility version to "+target.Version, log)
		if err := mongoFCV(ctx, dir, root, target.Version); err != nil {
			return undo(err)
		}
	}
	if in.Engine == MariaDB {
		s.step(in.ID, "Upgrading its system tables", log)
		if err := mariadbUpgrade(ctx, s.o.Runtime, in.ID, root, log); err != nil {
			return undo(err)
		}
	}
	if migrate {
		s.step(in.ID, "Restoring every database", log)
		for _, d := range dbs {
			if err := s.provision(ctx, in, d, root); err != nil {
				return undo(fmt.Errorf("making %s: %w", d.DbName, err))
			}
			if err := s.superRestore(ctx, in, root, d.DbName, filepath.Join(prev, "dumps", d.DbName+".sql"), log); err != nil {
				return undo(fmt.Errorf("restoring %s: %w", d.DbName, err))
			}
		}
		if err := s.reloadHBA(ctx, in, dir, root); err != nil {
			return undo(err)
		}
	}
	s.step(in.ID, "Checking every database", log)
	after, err := inventory(ctx, in, dir, root, dbs)
	if err != nil {
		return undo(fmt.Errorf("counting its rows after: %w", err))
	}
	if diff := compareInventory(before, after); diff != "" {
		return undo(errors.New(diff))
	}

	// Done. A patch keeps nothing to go back to; a version change keeps
	// the data from before.
	_ = os.RemoveAll(filepath.Join(prev, "dumps"))
	upd := store.SetInstanceVersionParams{Version: target.Version, Image: target.Image, UpdatedAt: time.Now().Unix(), ID: in.ID}
	if p.Patch {
		_ = os.RemoveAll(prev)
	} else {
		upd.PrevVersion, upd.PrevImage, upd.RollbackUntil = r.Version, r.Image, time.Now().Add(GoBackFor).Unix()
		_ = os.Remove(markerPath(dir))
	}
	if err := s.o.Store.Write.SetInstanceVersion(ctx, upd); err != nil {
		return err
	}
	s.setStatus(in.ID, StateRunning, nil)
	s.changed(ctx)
	_, _ = fmt.Fprintf(log, "done: %s %s (%s)\n", engineName(in.Engine), target.Version, target.Image)
	return nil
}

// putBack undoes a version change: the old data back in place, the old
// version in state.db, and the instance started again.
func (s *Service) putBack(ctx context.Context, id string, m marker, stopped, switched bool) error {
	ctx = context.WithoutCancel(ctx)
	dir := s.dir(id)
	prev := previousDir(dir)
	if switched {
		if err := s.o.Runtime.StopService(ctx, ContainerName(id), 2*time.Minute); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(prev, "data")); err != nil {
			return fmt.Errorf("the data from before is gone: %w", err)
		}
		if err := os.RemoveAll(dataDir(dir)); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(prev, "data"), dataDir(dir)); err != nil {
			return err
		}
	}
	if err := s.o.Store.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{
		Version: m.Version, Image: m.Image, UpdatedAt: time.Now().Unix(), ID: id,
	}); err != nil {
		return err
	}
	// The go-back point from an earlier change went when this one began.
	if err := os.RemoveAll(prev); err != nil {
		return err
	}
	if !stopped && !switched {
		return nil
	}
	return s.ensureLocked(ctx, id)
}

func (s *Service) goBackJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p VersionJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	defer s.lock(p.Instance)()
	in, r, err := s.instance(ctx, s.o.Store.Read, p.Instance)
	if err != nil {
		return nil, err
	}
	s.setChange(in.ID, &Change{JobID: j.ID, Kind: "back", From: in.Version, To: r.PrevVersion})
	err = s.goBack(ctx, in, r, log)
	s.finishChange(ctx, in, err)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": r.PrevVersion}, nil
}

func (s *Service) goBack(ctx context.Context, in Instance, r store.DatabaseInstance, log io.Writer) error {
	dir := s.dir(in.ID)
	prev := previousDir(dir)
	if in.Previous == "" {
		return invalid{"there's no earlier version to go back to"}
	}
	if _, err := os.Stat(filepath.Join(prev, "data")); err != nil {
		return fmt.Errorf("the data from before is gone: %w", err)
	}
	s.setStatus(in.ID, StateUpgrading, nil)
	s.step(in.ID, "Stopping it", log)
	if err := s.o.Runtime.StopService(ctx, ContainerName(in.ID), 2*time.Minute); err != nil {
		s.setStatus(in.ID, StateFailed, err)
		return err
	}
	s.step(in.ID, "Putting the data from before back", log)
	discard := filepath.Join(dir, "discarded")
	_ = os.RemoveAll(discard)
	if err := os.Rename(dataDir(dir), discard); err != nil {
		s.setStatus(in.ID, StateFailed, err)
		return err
	}
	if err := os.Rename(filepath.Join(prev, "data"), dataDir(dir)); err != nil {
		_ = os.Rename(discard, dataDir(dir))
		s.setStatus(in.ID, StateFailed, err)
		return err
	}
	if err := s.o.Store.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{
		Version: r.PrevVersion, Image: r.PrevImage, UpdatedAt: time.Now().Unix(), ID: in.ID,
	}); err != nil {
		s.setStatus(in.ID, StateFailed, err)
		return err
	}
	_ = os.RemoveAll(discard)
	_ = os.RemoveAll(prev)
	s.step(in.ID, "Starting "+engineName(in.Engine)+" "+r.PrevVersion, log)
	if err := s.ensureLocked(ctx, in.ID); err != nil {
		s.setStatus(in.ID, StateFailed, err)
		return err
	}
	s.setStatus(in.ID, StateRunning, nil)
	s.changed(ctx)
	_, _ = fmt.Fprintf(log, "done: back on %s %s\n", engineName(in.Engine), r.PrevVersion)
	return nil
}

// recover puts back a version change a Wings stop interrupted, before
// the instance starts.
func (s *Service) recover(ctx context.Context, id string) {
	dir := s.dir(id)
	b, err := os.ReadFile(markerPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var m marker
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err != nil {
		s.o.Log.Error("reading an interrupted version change failed", "instance", id, "err", err)
		return
	}
	s.o.Log.Warn("putting back a version change Wings' stop interrupted", "instance", id, "version", m.Version)
	ctx = context.WithoutCancel(ctx)
	prev := previousDir(dir)
	if m.Switched {
		if _, err := os.Stat(filepath.Join(prev, "data")); err == nil {
			_ = s.o.Runtime.StopService(ctx, ContainerName(id), 2*time.Minute)
			err := os.RemoveAll(dataDir(dir))
			if err == nil {
				err = os.Rename(filepath.Join(prev, "data"), dataDir(dir))
			}
			if err != nil {
				s.o.Log.Error("putting back an interrupted version change failed", "instance", id, "err", err)
				return
			}
		}
	}
	if err := s.o.Store.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{
		Version: m.Version, Image: m.Image, UpdatedAt: time.Now().Unix(), ID: id,
	}); err != nil {
		s.o.Log.Error("putting back an interrupted version change failed", "instance", id, "err", err)
		return
	}
	_ = os.RemoveAll(prev)
}

func (s *Service) instanceDatabases(ctx context.Context, id string) ([]store.Database, error) {
	all, err := s.o.Store.Read.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Database
	for _, d := range all {
		if d.InstanceID == id {
			out = append(out, d)
		}
	}
	return out, nil
}

// superDump dumps a PostgreSQL database as the superuser, with its owners
// and rights, into a file.
func (s *Service) superDump(ctx context.Context, in Instance, root, name, path string, log io.Writer) error {
	if err := checkNames(name); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // Wings' own folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var stderr tailBuffer
	code, err := s.o.Runtime.Exec(ctx, ContainerName(in.ID), containers.ExecSpec{
		Cmd: []string{"pg_dump", "--host=/var/run/postgresql", "--username=postgres", "--dbname=" + name},
		Env: []string{"PGPASSWORD=" + root, pgQuiet}, User: "999:999", Stdout: f, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return errors.New(lastLine(stderr.String()))
	}
	return f.Sync()
}

// superRestore runs a superDump into its database on the new version, in
// one transaction, stopping at the first error.
func (s *Service) superRestore(ctx context.Context, in Instance, root, name, path string, log io.Writer) error {
	f, err := os.Open(path) //nolint:gosec // Wings' own folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var stderr tailBuffer
	code, err := s.o.Runtime.Exec(ctx, ContainerName(in.ID), containers.ExecSpec{
		Cmd: []string{
			"psql", "--host=/var/run/postgresql", "--username=postgres", "--dbname=" + name,
			"--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1", "--single-transaction",
		},
		Env: []string{"PGPASSWORD=" + root, pgQuiet}, User: "999:999", Stdin: f, Stdout: io.Discard, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return errors.New(lastLine(stderr.String()))
	}
	return nil
}

// mariadbUpgrade upgrades MariaDB's system tables to the version running.
func mariadbUpgrade(ctx context.Context, rt Runtime, id, root string, log io.Writer) error {
	var stderr tailBuffer
	code, err := rt.Exec(ctx, ContainerName(id), containers.ExecSpec{
		Cmd: []string{"mariadb-upgrade", "--socket=/run/mysqld/mysqld.sock", "--user=root"},
		Env: []string{"MYSQL_PWD=" + root}, User: "999:999", Stdout: log, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("mariadb-upgrade failed: %s", lastLine(stderr.String()))
	}
	return nil
}

// inventory counts every table's rows in an instance's databases, by
// database and table.
func inventory(ctx context.Context, in Instance, dir, root string, dbs []store.Database) (map[string]int64, error) {
	switch in.Engine {
	case Redis, Valkey:
		return redisKeys(ctx, dir, root)
	case MongoDB:
		names := make([]string, 0, len(dbs))
		for _, d := range dbs {
			names = append(names, d.DbName)
		}
		return mongoCounts(ctx, dir, root, names)
	}
	out := map[string]int64{}
	for _, d := range dbs {
		if err := checkNames(d.DbName); err != nil {
			return nil, err
		}
		var db *sql.DB
		var list, count func(schema, table string) string
		if in.Engine == Postgres {
			db = connectTo(dir, root, d.DbName)
			list = func(string, string) string {
				return `SELECT schemaname, tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')`
			}
			count = func(schema, table string) string {
				return "SELECT count(*) FROM " + quotePG(schema) + "." + quotePG(table)
			}
		} else {
			var err error
			if db, err = connect(in, dir, root); err != nil {
				return nil, err
			}
			list = func(string, string) string {
				return "SELECT TABLE_SCHEMA, TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = '" + d.DbName + "' AND TABLE_TYPE = 'BASE TABLE'"
			}
			count = func(schema, table string) string {
				return "SELECT COUNT(*) FROM " + quoteMy(schema) + "." + quoteMy(table)
			}
		}
		err := func() error {
			defer func() { _ = db.Close() }()
			tables, err := listTables(ctx, db, list("", ""))
			if err != nil {
				return err
			}
			for _, t := range tables {
				var n int64
				if err := db.QueryRowContext(ctx, count(t[0], t[1])).Scan(&n); err != nil {
					return fmt.Errorf("%s.%s: %w", t[0], t[1], err)
				}
				out[d.DbName+": "+t[0]+"."+t[1]] = n
			}
			out[d.DbName+":"] = int64(len(tables)) // the database itself, with how many tables
			return nil
		}()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.DbName, err)
		}
	}
	return out, nil
}

func listTables(ctx context.Context, db *sql.DB, query string) ([][2]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out [][2]string
	for rows.Next() {
		var t [2]string
		if err := rows.Scan(&t[0], &t[1]); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func quoteMy(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

// compareInventory says what differs between two inventories; "" is
// nothing.
func compareInventory(before, after map[string]int64) string {
	var diffs []string
	for k, n := range before {
		m, ok := after[k]
		switch {
		case !ok && strings.HasSuffix(k, ":"):
			diffs = append(diffs, strings.TrimSuffix(k, ":")+" is missing")
		case !ok:
			diffs = append(diffs, k+" is missing")
		case strings.HasSuffix(k, ":") && n != m:
			diffs = append(diffs, fmt.Sprintf("%s has %d tables, not %d", strings.TrimSuffix(k, ":"), m, n))
		case n != m:
			diffs = append(diffs, fmt.Sprintf("%s has %d rows, not %d", k, m, n))
		}
	}
	if len(diffs) == 0 {
		return ""
	}
	slices.Sort(diffs)
	if len(diffs) > 5 {
		diffs = append(diffs[:5], fmt.Sprintf("and %d more", len(diffs)-5))
	}
	return "the databases weren't the same after: " + strings.Join(diffs, "; ")
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			n += fi.Size()
		}
		return nil
	})
	return n, err
}

// copyTree copies a folder with its owners and modes, links as links. On
// XFS the files' data is shared until changed (copy_file_range reflinks).
// The engine can write in src, so both sides are opened through os.Root:
// nothing it swaps for a link mid-copy is followed out of either.
func copyTree(src, dst string) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	from, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	return fs.WalkDir(from.FS(), ".", func(rel string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := from.Lstat(rel)
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if rel == "." {
				err = to.Chmod(".", fi.Mode().Perm())
			} else {
				err = to.Mkdir(rel, fi.Mode().Perm())
			}
		case fi.Mode()&fs.ModeSymlink != 0:
			var link string
			if link, err = from.Readlink(rel); err == nil {
				err = to.Symlink(link, rel)
			}
		case fi.Mode().IsRegular():
			err = copyFile(from, to, rel, fi.Mode().Perm())
		default:
			return nil // sockets and pipes
		}
		if err != nil {
			return err
		}
		// The modes as they were, not as Wings' umask leaves them.
		if !fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 || fi.IsDir() && rel != "." {
			if err := to.Chmod(rel, fi.Mode().Perm()); err != nil {
				return err
			}
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			if err := to.Lchown(rel, int(st.Uid), int(st.Gid)); err != nil && !errors.Is(err, fs.ErrPermission) {
				return err
			}
		}
		return nil
	})
}

func copyFile(from, to *os.Root, rel string, mode fs.FileMode) error {
	in, err := from.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is no longer a regular file", rel)
	}
	out, err := to.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// maintenanceRe is a maintenance window's start: an hour.
var maintenanceRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):00$`)

// DefaultMaintenance is when patch updates happen, in the node's time, if
// an instance doesn't say.
const DefaultMaintenance = "04:00"

// housekeeping, every minute: the data from before a change is removed
// once it's past its time, and instances on an older patch of their
// version get the newest at the start of their maintenance window, once
// a day at most. None is started while one can still go back: a patch
// would replace the data kept for that.
func (s *Service) housekeeping(ctx context.Context, now time.Time) {
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return
	}
	loc, err := time.LoadLocation(s.o.Timezone())
	if err != nil || s.o.Timezone() == "" {
		loc = time.UTC
	}
	local := now.In(loc)
	for _, r := range rows {
		if r.RollbackUntil != 0 && now.Unix() > r.RollbackUntil && s.state(r.ID) != StateUpgrading {
			s.expire(ctx, r.ID)
			continue
		}
		in, err := fromRow(r)
		if err != nil || !in.Update || in.Previous != "" || r.DesiredState != StateRunning || s.state(r.ID) != StateRunning {
			continue
		}
		if v, err := lookup(in.Engine, in.Version); err != nil || unavailable(v, s.kernel()) != "" {
			continue // a patch it couldn't start on
		}
		window := in.Settings.Maintenance
		if window == "" {
			window = DefaultMaintenance
		}
		if window == "off" || fmt.Sprintf("%02d:00", local.Hour()) != window {
			continue
		}
		today := local.Format(time.DateOnly)
		if r.PatchTried == today {
			continue
		}
		if err := s.o.Store.Write.SetInstancePatchTried(ctx, store.SetInstancePatchTriedParams{PatchTried: today, ID: r.ID}); err != nil {
			continue
		}
		if _, err := s.enqueueChange(ctx, in, in.Version, true); err != nil {
			s.o.Log.Warn("queueing a patch update failed", "instance", r.ID, "err", err)
		} else {
			s.o.Log.Info("patch update queued in the maintenance window", "instance", r.ID, "version", in.Version)
		}
	}
}

// expire removes the data kept from before a version change.
func (s *Service) expire(ctx context.Context, id string) {
	defer s.lock(id)()
	_, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil || r.RollbackUntil == 0 {
		return
	}
	if err := os.RemoveAll(previousDir(s.dir(id))); err != nil {
		s.o.Log.Warn("removing the data from before a version change failed", "instance", id, "err", err)
		return
	}
	_ = s.o.Store.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{
		Version: r.Version, Image: r.Image, UpdatedAt: time.Now().Unix(), ID: id,
	})
}

func (s *Service) loop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.housekeeping(ctx, now)
		}
	}
}
