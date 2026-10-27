//go:build e2e

// End-to-end tests against real engines in Docker (task e2e:runtime runs
// them in the Wings VM): each engine starts, makes databases and users, and
// keeps each user to its own database.
package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/store"
)

func TestEngines(t *testing.T) {
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
	nsvc := networks.New(networks.Options{Store: db, Runtime: dc, Log: slog.Default()})
	net, err := nsvc.Save(ctx, networks.Network{Name: "E2E databases", Internet: true, HostAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dc.RemoveNetwork(context.Background(), networks.DockerName(net.ID)) })
	data := filepath.Join(dir, "databases")
	ev := events.New(db)
	svc, err := New(Options{
		Store: db, Runtime: dc, Log: slog.Default(), Dir: data, KeyPath: filepath.Join(dir, "databases.key"),
		Networks: nsvc, StartTimeout: 5 * time.Minute, Events: ev,
	})
	if err != nil {
		t.Fatal(err)
	}
	one, two := "0199e2e0-0000-7000-8000-00000000a001", "0199e2e0-0000-7000-8000-00000000a002"
	for _, id := range []string{one, two} {
		if err := db.Write.InsertServer(ctx, store.InsertServerParams{
			ID: id, Name: id, Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
			Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + net.ID + `"]`,
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, engine := range []string{MariaDB, MySQL, Postgres} {
		t.Run(engine, func(t *testing.T) {
			in, err := svc.Save(ctx, InstanceInput{Name: "E2E " + engine, Engine: engine, Networks: []string{net.ID}, Limits: Limits{MemoryMiB: 512}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := svc.Delete(context.Background(), in.ID); err != nil {
					t.Errorf("delete: %v", err)
				}
				if _, err := os.Stat(filepath.Join(data, in.ID)); !os.IsNotExist(err) {
					t.Errorf("its files are still there: %v", err)
				}
			})
			in = waitRunning(t, svc, in.ID)
			addr := in.Addresses[net.ID]
			if a, err := netip.ParseAddr(addr); err != nil || !a.IsPrivate() {
				t.Fatalf("instance address %q", addr)
			}
			testHealth(t, svc, ev, in.ID)

			d1, pw1, err := svc.CreateDatabase(ctx, one, in.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			d2, pw2, err := svc.CreateDatabase(ctx, two, in.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := svc.CreateDatabase(ctx, one, in.ID, "main"); err == nil {
				t.Error("made a second database with the same name")
			}

			testBrowser(t, svc, one, d1, d2, engine)
			testReader(t, svc, one, d1, in, addr, engine)
			testStream(t, svc, one, d1, in, engine)
			testInstance(t, svc, in, d1, d2)

			// Each user reaches its own database over the network...
			if err := query(engine, addr, d1.User, pw1, d1.Name, "CREATE TABLE t (id INT PRIMARY KEY)"); err != nil {
				t.Fatalf("own database: %v", err)
			}
			// ...and not the other server's.
			if err := query(engine, addr, d1.User, pw1, d2.Name, "SELECT 1"); err == nil {
				t.Error("a user reached another server's database")
			}
			if err := query(engine, addr, d2.User, pw2, d1.Name, "SELECT 1"); err == nil {
				t.Error("a user reached another server's database")
			}
			// root never logs in over the network, even with its password.
			_, r, _ := svc.instance(ctx, db.Read, in.ID)
			root, _ := svc.sealer.open(r.RootPassword)
			rootUser := "root"
			if engine == Postgres {
				rootUser = "postgres"
			}
			if err := query(engine, addr, rootUser, root, "", "SELECT 1"); err == nil {
				t.Error("root logged in over the network")
			}

			// A new password works, the old one doesn't.
			pw3, err := svc.ResetPassword(ctx, one, d1.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := query(engine, addr, d1.User, pw1, d1.Name, "SELECT 1"); err == nil {
				t.Error("the old password still works")
			}
			if err := query(engine, addr, d1.User, pw3, d1.Name, "SELECT 1"); err != nil {
				t.Errorf("new password: %v", err)
			}
			if got, _ := svc.Password(ctx, one, d1.ID); got != pw3 {
				t.Error("Password isn't the new one")
			}
			env, err := svc.Env(ctx, one)
			if err != nil || !strings.Contains(strings.Join(env, "\n"), "DB_HOST="+in.Host) {
				t.Errorf("env %v %v", env, err)
			}

			// A restart keeps the data.
			if err := svc.Power(ctx, in.ID, "restart"); err != nil {
				t.Fatal(err)
			}
			waitRunning(t, svc, in.ID)
			if err := query(engine, addr, d1.User, pw3, d1.Name, "SELECT COUNT(*) FROM t"); err != nil {
				t.Errorf("after a restart: %v", err)
			}

			if err := svc.DeleteDatabase(ctx, one, d1.ID); err != nil {
				t.Fatal(err)
			}
			if err := query(engine, addr, d1.User, pw3, d1.Name, "SELECT 1"); err == nil {
				t.Error("a deleted database's user still logs in")
			}
		})
	}
}

func waitRunning(t *testing.T, svc *Service, id string) Instance {
	t.Helper()
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		in, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		switch in.State {
		case StateRunning:
			return in
		case StateFailed:
			t.Fatalf("instance failed: %s", in.Error)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("instance didn't start")
	return Instance{}
}

// query connects as a user over the network and runs one statement.
func query(engine, addr, user, pass, database, stmt string) error {
	var db *sql.DB
	if engine == Postgres {
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			return err
		}
		cfg.Host, cfg.Port, cfg.User, cfg.Password = addr, 5432, user, pass
		cfg.Database = database
		if database == "" {
			cfg.Database = "postgres"
		}
		cfg.ConnectTimeout = 5 * time.Second
		db = stdlib.OpenDB(*cfg)
	} else {
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = user, pass, "tcp", addr+":3306", database
		cfg.Timeout = 5 * time.Second
		c, err := mysql.NewConnector(cfg)
		if err != nil {
			return err
		}
		db = sql.OpenDB(c)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("%s as %s on %q: %w", stmt, user, database, err)
	}
	return nil
}

// testBrowser checks the browser on a fresh database: SQL, the schema,
// pages of rows, row edits, read-only queries that can't write, and the
// time limit.
func testBrowser(t *testing.T, svc *Service, server string, d, other Database, engine string) {
	t.Helper()
	ctx := context.Background()
	ts := "TIMESTAMP NULL"
	if engine == Postgres {
		ts = "TIMESTAMP"
	}
	res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "CREATE TABLE players (id INT PRIMARY KEY, name VARCHAR(40), joined " + ts + ");\n" +
		"INSERT INTO players (id, name) VALUES (1, 'alex'), (2, 'sam'), (3, 'charlie')"}, false)
	if err != nil {
		t.Fatalf("multi-statement SQL: %v", err)
	}
	if res.Affected != 3 {
		t.Errorf("the insert changed %d rows, want 3", res.Affected)
	}

	tables, err := svc.Schema(ctx, server, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "players" || len(tables[0].Columns) != 3 || strings.Join(tables[0].PrimaryKey, ",") != "id" {
		t.Fatalf("schema %+v", tables)
	}
	schema := tables[0].Schema

	page, err := svc.Rows(ctx, server, d.ID, RowsQuery{Schema: schema, Table: "players", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || !page.More || str(page.Rows[0][0]) != "1" || page.Rows[0][2] != nil {
		t.Errorf("first page %+v", page)
	}
	v := "a"
	page, err = svc.Rows(ctx, server, d.ID, RowsQuery{Schema: schema, Table: "players", Sort: "name", Desc: true, Filters: []Filter{{Column: "name", Op: "contains", Value: &v}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 3 || str(page.Rows[0][1]) != "sam" {
		t.Errorf("filtered and sorted %+v", page.Rows)
	}
	for _, bad := range []RowsQuery{
		{Table: "players; DROP TABLE players"},
		{Schema: schema, Table: "players", Sort: "name; DROP TABLE players"},
		{Schema: schema, Table: "players", Filters: []Filter{{Column: "nope", Op: "="}}},
		{Schema: schema, Table: "players", Filters: []Filter{{Column: "name", Op: "LIKE"}}},
	} {
		if _, err := svc.Rows(ctx, server, d.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	id, name, newName := "4", "dana", "dana2"
	if _, err := svc.EditRows(ctx, server, d.ID, "insert", RowEdit{Schema: schema, Table: "players", Values: map[string]*string{"id": &id, "name": &name}}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n, err := svc.EditRows(ctx, server, d.ID, "update", RowEdit{Schema: schema, Table: "players", Key: map[string]*string{"id": &id}, Values: map[string]*string{"name": &newName, "joined": nil}}); err != nil || n != 1 {
		t.Fatalf("update: %d %v", n, err)
	}
	if n, err := svc.EditRows(ctx, server, d.ID, "delete", RowEdit{Schema: schema, Table: "players", Keys: []map[string]*string{{"id": &id}}}); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err := svc.EditRows(ctx, server, d.ID, "delete", RowEdit{Schema: schema, Table: "players", Keys: []map[string]*string{{"id": &id}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("deleting a row that's gone: %v", err)
	}
	if _, err := svc.EditRows(ctx, server, d.ID, "update", RowEdit{Schema: schema, Table: "players", Key: map[string]*string{"id": &id}, Values: map[string]*string{"nope": &name}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("updating a column that isn't there: %v", err)
	}

	// Those who may only look can't change anything, however they try.
	if res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM players"}, true); err != nil || str(res.Rows[0][0]) != "3" {
		t.Errorf("read-only select: %+v %v", res, err)
	}
	for _, sneaky := range []string{
		"DELETE FROM players",
		"DROP TABLE players",
		"CREATE TABLE x (i INT)",
		"UPDATE players SET name = 'x'",
		"SELECT 1; DELETE FROM players",
		"COMMIT; DELETE FROM players",
	} {
		if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: sneaky}, true); err == nil {
			t.Errorf("read-only query ran %q", sneaky)
		}
	}
	if res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM players"}, true); err != nil || str(res.Rows[0][0]) != "3" {
		t.Errorf("after the read-only attempts: %+v %v", res, err)
	}

	// The other server's database is out of reach.
	cross := "SELECT * FROM `" + other.Name + "`.players"
	if engine == Postgres {
		cross = "SELECT * FROM dblink('dbname=" + other.Name + "', 'SELECT 1') AS t(i int)"
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: cross}, false); err == nil {
		t.Error("reached another server's database")
	}

	// A query past its time limit is stopped, and the next one runs.
	sleep := "SELECT SLEEP(5)"
	if engine == Postgres {
		sleep = "SELECT pg_sleep(5)"
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: sleep, Timeout: 1}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("a query past its time limit: %v", err)
	}
	if _, err := svc.Processes(ctx, server, d.ID); err != nil {
		t.Errorf("processes: %v", err)
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "DROP TABLE players"}, false); err != nil {
		t.Errorf("drop: %v", err)
	}
}

// testFiles is a server's files in a folder.
type testFiles struct{ dir string }

func (f testFiles) Open(_ context.Context, id string, _ bool) (*files.FS, error) {
	d := filepath.Join(f.dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	return files.Open(d, 0, 0, nil)
}

// TestTransfers exports databases and tables with the engines' own tools,
// imports the dumps back, and checks read-only users only read.
func TestTransfers(t *testing.T) {
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
	net, err := nsvc.Save(ctx, networks.Network{Name: "E2E transfers", Internet: true, HostAccess: true})
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
	server := "0199e2e0-0000-7000-8000-00000000b001"
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: server, Name: "s", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + net.ID + `"]`,
	}); err != nil {
		t.Fatal(err)
	}
	wait := func(t *testing.T, jobID string) Transfer {
		t.Helper()
		if _, err := engine.Wait(ctx, jobID); err != nil {
			t.Logf("wait: %v", err)
		}
		st, err := svc.TransferStatus(ctx, server, jobID)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	for _, eng := range []string{MariaDB, MySQL, Postgres} {
		t.Run(eng, func(t *testing.T) {
			in, err := svc.Save(ctx, InstanceInput{Name: "E2E transfers " + eng, Engine: eng, Networks: []string{net.ID}, Limits: Limits{MemoryMiB: 512}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Delete(context.Background(), in.ID) })
			in = waitRunning(t, svc, in.ID)
			addr := in.Addresses[net.ID]
			d, pw, err := svc.CreateDatabase(ctx, server, in.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "CREATE TABLE players (id INT PRIMARY KEY, name VARCHAR(40));\n" +
				"INSERT INTO players VALUES (1, 'alex'), (2, 'sam, \"the\" builder')"}, false); err != nil {
				t.Fatal(err)
			}

			// A whole database as .sql.gz.
			jobID, path, err := svc.Export(ctx, server, d.ID, "", "", "sql")
			if err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "succeeded" {
				t.Fatalf("export: %+v", st)
			}
			raw, err := os.ReadFile(filepath.Join(fdir, server, path))
			if err != nil {
				t.Fatal(err)
			}
			gz, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			dump, _ := io.ReadAll(gz)
			if !strings.Contains(string(dump), "players") || !strings.Contains(string(dump), "alex") {
				t.Fatalf("the dump has no players:\n%s", dump)
			}
			if _, err := os.Stat(filepath.Join(fdir, server, path+".part")); !os.IsNotExist(err) {
				t.Error("the partial file is left")
			}

			// One table as CSV.
			jobID, csvPath, err := svc.Export(ctx, server, d.ID, "", "players", "csv")
			if err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "succeeded" {
				t.Fatalf("csv export: %+v", st)
			}
			csvText, _ := os.ReadFile(filepath.Join(fdir, server, csvPath))
			if want := "id,name\n1,alex\n2,\"sam, \"\"the\"\" builder\"\n"; string(csvText) != want {
				t.Errorf("csv %q, want %q", csvText, want)
			}
			if _, _, err := svc.Export(ctx, server, d.ID, "", "nope", "csv"); !errors.Is(err, ErrInvalid) {
				t.Errorf("exporting a table that isn't there: %v", err)
			}

			// A query's whole result as CSV, run as the database's reader:
			// one that would change something fails, and changes nothing.
			jobID, qPath, err := svc.ExportQuery(ctx, server, d.ID, QueryInput{SQL: "SELECT name FROM players ORDER BY id"})
			if err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "succeeded" {
				t.Fatalf("query export: %+v", st)
			}
			qText, _ := os.ReadFile(filepath.Join(fdir, server, qPath))
			if want := "name\nalex\n\"sam, \"\"the\"\" builder\"\n"; string(qText) != want {
				t.Errorf("query csv %q, want %q", qText, want)
			}
			if jobID, _, err = svc.ExportQuery(ctx, server, d.ID, QueryInput{SQL: "DELETE FROM players"}); err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "failed" {
				t.Errorf("an export of a DELETE: %+v", st)
			}
			if res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM players"}, true); err != nil || str(res.Rows[0][0]) != "2" {
				t.Errorf("after exporting a DELETE: %+v %v", res, err)
			}

			// The dump imports back.
			if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "DROP TABLE players"}, false); err != nil {
				t.Fatal(err)
			}
			jobID, err = svc.Import(ctx, server, d.ID, path)
			if err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "succeeded" {
				t.Fatalf("import: %+v", st)
			}
			res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM players"}, true)
			if err != nil || str(res.Rows[0][0]) != "2" {
				t.Fatalf("after the import: %+v %v", res, err)
			}
			// A broken file fails, and says where.
			bad := filepath.Join(fdir, server, "broken.sql")
			if err := os.WriteFile(bad, []byte("INSERT INTO nowhere VALUES (1);\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			jobID, err = svc.Import(ctx, server, d.ID, "broken.sql")
			if err != nil {
				t.Fatal(err)
			}
			if st := wait(t, jobID); st.Status != "failed" || !strings.Contains(st.Error, "nowhere") {
				t.Errorf("a broken import: %+v", st)
			}

			// A read-only user reads every table, now and later, and writes none.
			user, rpw, err := svc.SetReadOnly(ctx, server, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "SELECT * FROM players"); err != nil {
				t.Errorf("read-only select: %v", err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "INSERT INTO players VALUES (3, 'x')"); err == nil {
				t.Error("the read-only user inserted")
			}
			if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "CREATE TABLE later (id INT PRIMARY KEY)"}, false); err != nil {
				t.Fatal(err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "SELECT * FROM later"); err != nil {
				t.Errorf("read-only select of a table made later: %v", err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "DROP TABLE later"); err == nil {
				t.Error("the read-only user dropped a table")
			}
			if got, _ := svc.ReadOnlyPassword(ctx, server, d.ID); got != rpw {
				t.Error("ReadOnlyPassword isn't the one made")
			}
			if err := svc.RemoveReadOnly(ctx, server, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "SELECT 1"); err == nil {
				t.Error("a removed read-only user still logs in")
			}
			// Deleting a database takes its read-only user with it.
			if _, _, err := svc.SetReadOnly(ctx, server, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.DeleteDatabase(ctx, server, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := query(eng, addr, user, rpw, d.Name, "SELECT 1"); err == nil {
				t.Error("a deleted database's read-only user still logs in")
			}
			_ = pw
		})
	}
}
