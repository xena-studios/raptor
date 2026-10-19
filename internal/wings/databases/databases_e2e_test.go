//go:build e2e

// End-to-end tests against real engines in Docker (task e2e:runtime runs
// them in the Wings VM): each engine starts, makes databases and users, and
// keeps each user to its own database.
package databases

import (
	"context"
	"database/sql"
	"fmt"
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
	svc, err := New(Options{
		Store: db, Runtime: dc, Log: slog.Default(), Dir: data, KeyPath: filepath.Join(dir, "databases.key"),
		Networks: nsvc, StartTimeout: 5 * time.Minute,
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
