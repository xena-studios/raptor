//go:build e2e

package databases

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// TestMain runs the tests with Wings' umask (UMask=0077 in its unit), so
// anything the engines read that Wings writes is tested as it is on a node.
func TestMain(m *testing.M) {
	syscall.Umask(0o077)
	os.Exit(m.Run())
}

// e2e is a service on real Docker with jobs, a network, and one server on
// it.
type e2e struct {
	svc    *Service
	db     *store.DB
	jobs   *jobs.Engine
	net    networks.Network
	fdir   string // the servers' files
	server string
}

func newE2E(t *testing.T, name string) e2e {
	t.Helper()
	ctx := context.Background()
	dc, err := docker.New(docker.Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	engine := jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(dir, "jobs")})
	nsvc := networks.New(networks.Options{Store: db, Runtime: dc})
	net, err := nsvc.Save(ctx, networks.Network{Name: name, Internet: true, HostAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.RemoveNetwork(context.Background(), networks.DockerName(net.ID)) })
	svc, err := New(Options{
		Store: db, Runtime: dc, Dir: filepath.Join(dir, "databases"), KeyPath: filepath.Join(dir, "databases.key"),
		Networks: nsvc, Jobs: engine, StartTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(dir, "servers")
	svc.SetFiles(testFiles{fdir})
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	server := "0199e2e0-0000-7000-8000-00000000c001"
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: server, Name: "s", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + net.ID + `"]`,
	}); err != nil {
		t.Fatal(err)
	}
	return e2e{svc: svc, db: db, jobs: engine, net: net, fdir: fdir, server: server}
}

func (e e2e) wait(t *testing.T, jobID string) jobs.Job {
	t.Helper()
	j, err := e.jobs.Wait(context.Background(), jobID)
	if err != nil {
		t.Logf("wait: %v", err)
	}
	return j
}

func (e e2e) count(t *testing.T, d Database, table string) string {
	t.Helper()
	res, err := e.svc.Query(context.Background(), e.server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM " + table}, true)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return str(res.Rows[0][0])
}

// TestVersions changes instances' versions in place (MariaDB, MySQL), by
// migration (PostgreSQL), and back; and checks a change that fails is put
// back as it was.
func TestVersions(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E versions")
	for _, c := range []struct{ engine, from, to string }{
		{MariaDB, "11.4", "11.8"},
		{MySQL, "8.0", "8.4"},
		{Postgres, "16", "17"},
	} {
		t.Run(c.engine, func(t *testing.T) {
			in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E versions " + c.engine, Engine: c.engine, Version: c.from, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 768}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
			in = waitRunning(t, e.svc, in.ID)
			addr := in.Addresses[e.net.ID]
			d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "CREATE TABLE players (id INT PRIMARY KEY, name VARCHAR(40));\n" +
				"INSERT INTO players VALUES (1, 'alex'), (2, 'sam')"}, false); err != nil {
				t.Fatal(err)
			}
			ro, rpw, err := e.svc.SetReadOnly(ctx, e.server, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.ChangeVersion(ctx, in.ID, "10.0"); err == nil {
				t.Error("went down a version")
			}

			jobID, err := e.svc.ChangeVersion(ctx, in.ID, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "during"); err == nil {
				t.Error("made a database during the change")
			}
			if j := e.wait(t, jobID); j.Status != "succeeded" {
				t.Fatalf("the change failed: %s", j.Error)
			}
			got, err := e.svc.Get(ctx, in.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != c.to || got.State != StateRunning || got.Previous != c.from || got.Change == nil || !got.Change.Done || got.Change.Error != "" {
				t.Fatalf("after the change: %+v %+v", got, got.Change)
			}
			if e.count(t, d, "players") != "2" {
				t.Error("rows lost")
			}
			// The users and their passwords carried over.
			if err := query(c.engine, addr, d.User, pw, d.Name, "INSERT INTO players VALUES (3, 'new')"); err != nil {
				t.Errorf("the database's user after the change: %v", err)
			}
			if err := query(c.engine, addr, ro, rpw, d.Name, "SELECT * FROM players"); err != nil {
				t.Errorf("the read-only user after the change: %v", err)
			}
			if err := query(c.engine, addr, ro, rpw, d.Name, "DELETE FROM players"); err == nil {
				t.Error("the read-only user can write after the change")
			}
			version := "SELECT VERSION()"
			res, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: version}, true)
			if err != nil || !strings.Contains(str(res.Rows[0][0]), c.to) {
				t.Errorf("the engine says %v %v", res, err)
			}

			// Going back: the old version, with the data from before the
			// change (the row added since is gone).
			jobID, err = e.svc.GoBack(ctx, in.ID)
			if err != nil {
				t.Fatal(err)
			}
			if j := e.wait(t, jobID); j.Status != "succeeded" {
				t.Fatalf("going back failed: %s", j.Error)
			}
			got, _ = e.svc.Get(ctx, in.ID)
			if got.Version != c.from || got.Previous != "" || got.State != StateRunning {
				t.Fatalf("after going back: %+v", got)
			}
			if n := e.count(t, d, "players"); n != "2" {
				t.Errorf("after going back, %s rows, want the 2 from before the change", n)
			}
			if _, err := os.Stat(previousDir(e.svc.dir(in.ID))); !os.IsNotExist(err) {
				t.Errorf("the data from before is still kept: %v", err)
			}
		})
	}

	t.Run("failed change", func(t *testing.T) {
		// A version whose image isn't MariaDB: it can't start, so the
		// change is put back.
		Versions = append(Versions, Version{MariaDB, "99.0", "busybox:1.37", false})
		t.Cleanup(func() { Versions = Versions[:len(Versions)-1] })
		in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E failed change", Engine: MariaDB, Version: "11.8", Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 512}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
		in = waitRunning(t, e.svc, in.ID)
		d, _, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "keep")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "CREATE TABLE t (id INT PRIMARY KEY); INSERT INTO t VALUES (1)"}, false); err != nil {
			t.Fatal(err)
		}
		e.svc.o.StartTimeout = 30 * time.Second
		t.Cleanup(func() { e.svc.o.StartTimeout = 5 * time.Minute })
		jobID, err := e.svc.ChangeVersion(ctx, in.ID, "99.0")
		if err != nil {
			t.Fatal(err)
		}
		if j := e.wait(t, jobID); j.Status != "failed" || !strings.Contains(j.Error, "back on MariaDB 11.8") {
			t.Fatalf("the change: %s %s", j.Status, j.Error)
		}
		got, _ := e.svc.Get(ctx, in.ID)
		if got.Version != "11.8" || got.State != StateRunning || got.Previous != "" || got.Change == nil || got.Change.Error == "" {
			t.Fatalf("after the failed change: %+v %+v", got, got.Change)
		}
		if e.count(t, d, "t") != "1" {
			t.Error("rows lost")
		}
	})

	t.Run("patch in the maintenance window", func(t *testing.T) {
		in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E patch", Engine: MariaDB, Version: "11.8", Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 512}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
		waitRunning(t, e.svc, in.ID)
		// As if it was made by an older Wings, on an older patch.
		if err := e.db.Write.SetInstanceVersion(ctx, store.SetInstanceVersionParams{Version: "11.8", Image: "mariadb:11.8", ID: in.ID}); err != nil {
			t.Fatal(err)
		}
		got, _ := e.svc.Get(ctx, in.ID)
		if !got.Update {
			t.Fatal("no update shown")
		}
		now := time.Now().UTC()
		in.Settings.Maintenance = now.Format("15") + ":00"
		if _, err := e.svc.Save(ctx, InstanceInput{ID: in.ID, Name: in.Name, Networks: in.Networks, Limits: in.Limits, Settings: in.Settings}); err != nil {
			t.Fatal(err)
		}
		waitRunning(t, e.svc, in.ID)
		e.svc.housekeeping(ctx, now)
		got, _ = e.svc.Get(ctx, in.ID)
		if got.Change == nil || got.Change.Kind != "patch" {
			t.Fatalf("no patch queued: %+v", got.Change)
		}
		patchJob := got.Change.JobID
		if j := e.wait(t, patchJob); j.Status != "succeeded" {
			t.Fatalf("the patch failed: %s", j.Error)
		}
		got, _ = e.svc.Get(ctx, in.ID)
		if got.Update || got.Previous != "" || got.Image != latest(MariaDB, "11.8") {
			t.Errorf("after the patch: %+v", got)
		}
		// Once a day at most.
		e.svc.housekeeping(ctx, now)
		if got, _ := e.svc.Get(ctx, in.ID); got.Change.JobID != patchJob || !got.Change.Done {
			t.Error("a second patch was queued the same day")
		}
	})
}

// fakeBackups hands out dumps from a folder, as a backup would.
type fakeBackups struct{ dumps string }

func (f fakeBackups) ExtractDatabase(_ context.Context, _, _, _, file, dir string, _ io.Writer) (string, error) {
	b, err := os.ReadFile(filepath.Join(f.dumps, file))
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "dump", DumpsFolder, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o600)
}

// TestBackupDumps dumps a server's databases as a backup does, restores
// them as a restore of the backup does, and restores one database alone.
func TestBackupDumps(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E backup dumps")
	for _, eng := range []string{MariaDB, MySQL, Postgres} {
		t.Run(eng, func(t *testing.T) {
			in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E dumps " + eng, Engine: eng, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 512}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
			in = waitRunning(t, e.svc, in.ID)
			addr := in.Addresses[e.net.ID]
			d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "perms")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.DeleteDatabase(context.Background(), e.server, d.ID) })
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "CREATE TABLE ranks (id INT PRIMARY KEY, name VARCHAR(40));\n" +
				"INSERT INTO ranks VALUES (1, 'admin'), (2, 'mod'), (3, 'member')"}, false); err != nil {
				t.Fatal(err)
			}
			ro, rpw, err := e.svc.SetReadOnly(ctx, e.server, d.ID)
			if err != nil {
				t.Fatal(err)
			}

			dir, names, warning, err := e.svc.Dump(ctx, e.server, io.Discard)
			if err != nil || warning != "" || len(names) != 1 || names[0] != "perms" {
				t.Fatalf("dump: %q %v %q %v", dir, names, warning, err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			dump, _ := os.ReadFile(filepath.Join(dir, "perms.sql"))
			if !strings.Contains(string(dump), "admin") {
				t.Fatalf("the dump has no rows:\n%s", dump)
			}

			// A restore of the server's files brings the dumps back; they're
			// run into the databases, emptied first.
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "DELETE FROM ranks WHERE id > 1; CREATE TABLE junk (id INT PRIMARY KEY)"}, false); err != nil {
				t.Fatal(err)
			}
			restored := t.TempDir()
			if err := os.MkdirAll(filepath.Join(restored, DumpsFolder), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(restored, DumpsFolder, "perms.sql"), dump, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(restored, DumpsFolder, "gone.sql"), []byte("SELECT 1;"), 0o644); err != nil {
				t.Fatal(err)
			}
			warning, err = e.svc.Restore(ctx, e.server, restored, io.Discard)
			if err != nil || !strings.Contains(warning, "gone isn't one of the server's databases") {
				t.Fatalf("restore: %q %v", warning, err)
			}
			if n := e.count(t, d, "ranks"); n != "3" {
				t.Errorf("after the restore, %s ranks", n)
			}
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "SELECT * FROM junk"}, true); err == nil {
				t.Error("a table made after the backup is still there")
			}
			if _, err := os.Stat(filepath.Join(restored, DumpsFolder, "perms.sql")); !os.IsNotExist(err) {
				t.Error("a restored dump was left")
			}
			if _, err := os.Stat(filepath.Join(restored, DumpsFolder, "gone.sql")); err != nil {
				t.Error("the dump of a database the server doesn't have was removed")
			}
			if err := query(eng, addr, d.User, pw, d.Name, "INSERT INTO ranks VALUES (4, 'vip')"); err != nil {
				t.Errorf("the database's user after the restore: %v", err)
			}
			if err := query(eng, addr, ro, rpw, d.Name, "SELECT * FROM ranks"); err != nil {
				t.Errorf("the read-only user after the restore: %v", err)
			}

			// One database alone, from a backup: what it had is exported first.
			e.svc.SetBackups(fakeBackups{dumps: dir})
			jobID, before, err := e.svc.RestoreDatabase(ctx, e.server, d.ID, "b1")
			if err != nil {
				t.Fatal(err)
			}
			if j := e.wait(t, jobID); j.Status != "succeeded" {
				t.Fatalf("db.restore: %s", j.Error)
			}
			if n := e.count(t, d, "ranks"); n != "3" {
				t.Errorf("after db.restore, %s ranks (the vip added since should be gone)", n)
			}
			if _, err := os.Stat(filepath.Join(e.fdir, e.server, before)); err != nil {
				t.Errorf("the export from before: %v", err)
			}
			st, err := e.svc.TransferStatus(ctx, e.server, jobID)
			if err != nil || st.Kind != "restore" || st.Path != before {
				t.Errorf("status: %+v %v", st, err)
			}
		})
	}
}
