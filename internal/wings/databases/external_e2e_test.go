//go:build e2e

package databases

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	oexec "os/exec"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/xena-studios/raptor/internal/wings/networks"
)

// hostIP is the node's own address on its default route: connections
// from the node to it reach the external port as if from outside (the
// source isn't one of the instance's networks).
func hostIP(t *testing.T) string {
	t.Helper()
	c, err := net.Dial("udp", "192.0.2.1:9") //nolint:noctx // nothing is sent
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// dial connects to host:port as a user, with TLS checked against ca if
// it's set, and runs a statement.
func dial(engine, host string, port int, user, pass, database string, tc *tls.Config, stmt string) error {
	var db *sql.DB
	if engine == Postgres {
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			return err
		}
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = host, uint16(port), user, pass, database //nolint:gosec // a test port
		cfg.ConnectTimeout = 5 * time.Second
		cfg.TLSConfig, cfg.Fallbacks = tc, nil
		db = stdlib.OpenDB(*cfg)
	} else {
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = user, pass, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), database
		cfg.Timeout = 5 * time.Second
		if tc != nil {
			name := fmt.Sprintf("e2e-%p", tc)
			if err := mysql.RegisterTLSConfig(name, tc); err != nil {
				return err
			}
			cfg.TLSConfig = name
		}
		c, err := mysql.NewConnector(cfg)
		if err != nil {
			return err
		}
		db = sql.OpenDB(c)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, stmt)
	return err
}

// fromNetwork runs the engine's own client in a container on a network,
// without TLS, as a server's plugin would connect.
func fromNetwork(in Instance, network, host, user, pass, database string) error {
	var args []string
	switch in.Engine {
	case MariaDB:
		args = []string{"mariadb", "--skip-ssl", "-h", host, "-u", user, "-p" + pass, database, "-e", "SELECT 1"}
	case MySQL:
		args = []string{"mysql", "--ssl-mode=DISABLED", "--get-server-public-key", "-h", host, "-u", user, "-p" + pass, database, "-e", "SELECT 1"}
	default:
		args = []string{"psql", "postgresql://" + user + ":" + pass + "@" + host + "/" + database + "?sslmode=disable", "-c", "SELECT 1"}
	}
	out, err := oexec.Command("docker", append([]string{"run", "--rm", "--network", network, "--entrypoint", args[0], in.Image}, args[1:]...)...).CombinedOutput() //nolint:gosec,noctx // a test
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// TestExternal gives instances external access: outside connections need
// TLS (and get the instance's certificate), servers on its network still
// connect without, and turning it off puts everything back.
func TestExternal(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E external")
	ip := hostIP(t)
	for i, eng := range []string{MariaDB, MySQL, Postgres} {
		t.Run(eng, func(t *testing.T) {
			in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E external " + eng, Engine: eng, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 512}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
			in = waitRunning(t, e.svc, in.ID)
			d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "CREATE TABLE t (id INT PRIMARY KEY)"}, false); err != nil {
				t.Fatal(err)
			}
			port := 39100 + i
			if _, err := e.svc.SetExternal(ctx, ExternalInput{ID: in.ID, Port: port}); !errors.Is(err, ErrInvalid) {
				t.Errorf("no allowlist: %v", err)
			}
			if _, err := e.svc.SetExternal(ctx, ExternalInput{ID: in.ID, Port: 80, Allow: []string{"0.0.0.0/0"}}); !errors.Is(err, ErrInvalid) {
				t.Errorf("a low port: %v", err)
			}
			if _, err := e.svc.SetExternal(ctx, ExternalInput{ID: in.ID, Port: port, Allow: []string{"0.0.0.0/0"}}); err != nil {
				t.Fatal(err)
			}
			in = waitRunning(t, e.svc, in.ID)
			if in.External == nil || in.External.Port != port || in.External.CA == "" {
				t.Fatalf("external: %+v", in.External)
			}
			// A user made after it's on gets its accounts too.
			ro, rpw, err := e.svc.SetReadOnly(ctx, e.server, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(in.External.CA)) {
				t.Fatal("the CA isn't PEM")
			}
			tc := &tls.Config{RootCAs: pool, ServerName: in.Host, MinVersion: tls.VersionTLS12}

			// From outside: TLS, checked against the instance's authority.
			if err := dial(eng, ip, port, d.User, pw, d.Name, tc, "INSERT INTO t VALUES (1)"); err != nil {
				t.Errorf("from outside with TLS: %v", err)
			}
			if err := dial(eng, ip, port, ro, rpw, d.Name, tc, "SELECT * FROM t"); err != nil {
				t.Errorf("the read-only user from outside with TLS: %v", err)
			}
			if err := dial(eng, ip, port, d.User, pw, d.Name, nil, "SELECT 1"); err == nil {
				t.Error("connected from outside without TLS")
			}
			// From a server's network: no TLS needed, as before.
			if err := fromNetwork(in, networks.DockerName(e.net.ID), in.Host, d.User, pw, d.Name); err != nil {
				t.Errorf("from the network without TLS: %v", err)
			}
			// Another instance can't have the port.
			other, err := e.svc.Save(ctx, InstanceInput{Name: "E2E external other " + eng, Engine: eng, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 256}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.Delete(context.Background(), other.ID) })
			if _, err := e.svc.SetExternal(ctx, ExternalInput{ID: other.ID, Port: port, Allow: []string{"0.0.0.0/0"}}); !errors.Is(err, ErrInvalid) {
				t.Errorf("a second instance on the port: %v", err)
			}

			// Off: no port, and the users back to one account each.
			if _, err := e.svc.SetExternal(ctx, ExternalInput{ID: in.ID}); err != nil {
				t.Fatal(err)
			}
			in = waitRunning(t, e.svc, in.ID)
			if in.External != nil {
				t.Errorf("still external: %+v", in.External)
			}
			if err := dial(eng, ip, port, d.User, pw, d.Name, tc, "SELECT 1"); err == nil {
				t.Error("the port still answers")
			}
			if eng != Postgres {
				_, r, _ := e.svc.instance(ctx, e.db.Read, in.ID)
				root, _ := e.svc.sealer.open(r.RootPassword)
				db, err := connect(in, e.svc.dir(in.ID), root)
				if err != nil {
					t.Fatal(err)
				}
				hosts, err := userHosts(ctx, db, d.User)
				_ = db.Close()
				if err != nil || !slices.Equal(hosts, []string{"%"}) {
					t.Errorf("accounts after turning it off: %v %v", hosts, err)
				}
			}
			if err := fromNetwork(in, networks.DockerName(e.net.ID), in.Host, d.User, pw, d.Name); err != nil {
				t.Errorf("from the network after: %v", err)
			}
		})
	}
}
