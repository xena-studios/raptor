package databases

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// The engines' official images run as uid and gid 999 once their
// entrypoint has set the data folder up.
const engineUID = 999

// An instance's folder on the server data volume:
//
//	data/  the engine's data
//	run/   its socket, which Wings connects through (never the network)
//	conf/  configuration Wings writes (PostgreSQL's pg_hba.conf)
func dataDir(dir string) string { return filepath.Join(dir, "data") }
func runDir(dir string) string  { return filepath.Join(dir, "run") }
func confDir(dir string) string { return filepath.Join(dir, "conf") }

// pgHBA lets each user reach only the database named like it (sameuser),
// over the network; the superuser only through the socket, which only
// Wings reaches (docs/DATABASES.md#isolation).
const pgHBA = `# Written by Raptor Wings; changes are overwritten.
local all all scram-sha-256
host all postgres all reject
host sameuser all all scram-sha-256
`

// serviceSpec is an instance's container.
func serviceSpec(in Instance, dir, rootPassword, timezone string) containers.ServiceSpec {
	mem := in.Limits.MemoryMiB
	s := containers.ServiceSpec{
		Name:        ContainerName(in.ID),
		Role:        Role,
		ID:          in.ID,
		Image:       in.Image,
		Tmpfs:       []string{"/tmp"},
		MemoryMiB:   mem,
		CPUPercent:  in.Limits.CPUPercent,
		StopTimeout: 2 * time.Minute,
	}
	maxConn := in.Settings.MaxConnections
	if maxConn == 0 {
		maxConn = defaultMaxConnections
	}
	tz := in.Settings.Timezone
	if tz == "" {
		tz = timezone
	}
	if tz != "" {
		s.Env = append(s.Env, "TZ="+tz)
	}
	switch in.Engine {
	case MariaDB, MySQL:
		pre := "MARIADB_"
		sock := "/run/mysqld"
		if in.Engine == MySQL {
			pre, sock = "MYSQL_", "/var/run/mysqld"
		}
		// root only through the socket, never over the network.
		s.Env = append(s.Env, pre+"ROOT_PASSWORD="+rootPassword, pre+"ROOT_HOST=localhost")
		s.Mounts = []containers.Mount{
			{Source: dataDir(dir), Target: "/var/lib/mysql"},
			{Source: runDir(dir), Target: sock},
		}
		s.Cmd = []string{
			"--max-connections=" + strconv.Itoa(maxConn),
			"--innodb-buffer-pool-size=" + strconv.FormatInt(max(mem/2, 64), 10) + "M",
			"--character-set-server=utf8mb4",
			"--collation-server=utf8mb4_unicode_ci",
			"--skip-name-resolve",
		}
		// The performance schema alone takes hundreds of MB on MySQL.
		if mem < 1024 {
			s.Cmd = append(s.Cmd, "--performance-schema=OFF")
		}
		// No reading or writing the container's files from SQL (users
		// never get FILE anyway).
		if in.Engine == MySQL {
			s.Cmd = append(s.Cmd, "--secure-file-priv=NULL")
		}
	case Postgres:
		s.Env = append(s.Env, "POSTGRES_PASSWORD="+rootPassword, "PGDATA=/var/lib/postgresql/data")
		s.Mounts = []containers.Mount{
			{Source: dataDir(dir), Target: "/var/lib/postgresql/data"},
			{Source: runDir(dir), Target: "/var/run/postgresql"},
			{Source: confDir(dir), Target: "/etc/raptor", ReadOnly: true},
		}
		s.Cmd = []string{
			"postgres",
			"-c", "max_connections=" + strconv.Itoa(maxConn),
			"-c", "shared_buffers=" + strconv.FormatInt(max(mem/4, 32), 10) + "MB",
			"-c", "effective_cache_size=" + strconv.FormatInt(max(mem*3/4, 64), 10) + "MB",
			"-c", "hba_file=/etc/raptor/pg_hba.conf",
		}
		if tz != "" {
			s.Cmd = append(s.Cmd, "-c", "timezone="+tz, "-c", "log_timezone="+tz)
		}
	}
	return s
}

const defaultMaxConnections = 100

// connect opens an admin connection to an instance through its socket.
func connect(in Instance, dir, rootPassword string) (*sql.DB, error) {
	switch in.Engine {
	case MariaDB, MySQL:
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd = "root", rootPassword
		cfg.Net, cfg.Addr = "unix", filepath.Join(runDir(dir), "mysqld.sock")
		cfg.Timeout = 5 * time.Second
		c, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(c), nil
	case Postgres:
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			return nil, err
		}
		cfg.Host, cfg.Port = runDir(dir), 5432
		cfg.User, cfg.Password, cfg.Database = "postgres", rootPassword, "postgres"
		cfg.ConnectTimeout = 5 * time.Second
		// Utility statements (CREATE ROLE …) take no parameters, so they're
		// sent as they are.
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		return stdlib.OpenDB(*cfg), nil
	}
	return nil, fmt.Errorf("unknown engine %q", in.Engine)
}

// connectTo opens an admin connection to one database on a PostgreSQL
// instance.
func connectTo(dir, rootPassword, database string) *sql.DB {
	cfg, _ := pgx.ParseConfig("")
	cfg.Host, cfg.Port = runDir(dir), 5432
	cfg.User, cfg.Password, cfg.Database = "postgres", rootPassword, database
	cfg.ConnectTimeout = 5 * time.Second
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return stdlib.OpenDB(*cfg)
}

// Names are checked before they go into SQL: lowercase letters, digits,
// and underscores, starting with a letter. Passwords are letters and
// digits (password). Both are then quoted anyway.
var safeName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func checkNames(names ...string) error {
	for _, n := range names {
		if !safeName.MatchString(n) {
			return fmt.Errorf("unsafe name %q", n)
		}
	}
	return nil
}

func checkPassword(p string) error {
	for _, r := range p {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("unsafe password")
		}
	}
	return nil
}

// createDatabase makes a database and its user, with rights to it alone.
// It's idempotent, so a create interrupted halfway can run again.
func createDatabase(ctx context.Context, in Instance, dir, rootPassword, name, user, pass string) error {
	if err := checkNames(name, user); err != nil {
		return err
	}
	if err := checkPassword(pass); err != nil {
		return err
	}
	db, err := connect(in, dir, rootPassword)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	switch in.Engine {
	case MariaDB, MySQL:
		// ALL on the one database: no GRANT OPTION, nothing global (no
		// FILE, SUPER, or PROCESS).
		return exec(ctx, db,
			"CREATE DATABASE IF NOT EXISTS `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
			"CREATE USER IF NOT EXISTS '"+user+"'@'%' IDENTIFIED BY '"+pass+"'",
			"ALTER USER '"+user+"'@'%' IDENTIFIED BY '"+pass+"'",
			"GRANT ALL PRIVILEGES ON `"+name+"`.* TO '"+user+"'@'%'",
		)
	case Postgres:
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '"+user+"')").Scan(&exists); err != nil {
			return err
		}
		role := `CREATE ROLE "` + user + `" LOGIN PASSWORD '` + pass + `'`
		if exists {
			role = `ALTER ROLE "` + user + `" LOGIN PASSWORD '` + pass + `'`
		}
		if err := exec(ctx, db, role); err != nil {
			return err
		}
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = '"+name+"')").Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := exec(ctx, db, `CREATE DATABASE "`+name+`" OWNER "`+user+`" ENCODING 'UTF8'`); err != nil {
				return err
			}
		}
		if err := exec(ctx, db, `REVOKE ALL ON DATABASE "`+name+`" FROM PUBLIC`); err != nil {
			return err
		}
		// The public schema too: PostgreSQL 14 lets everyone create in it.
		inner := connectTo(dir, rootPassword, name)
		defer func() { _ = inner.Close() }()
		return exec(ctx, inner, `ALTER SCHEMA public OWNER TO "`+user+`"`, `REVOKE ALL ON SCHEMA public FROM PUBLIC`)
	}
	return fmt.Errorf("unknown engine %q", in.Engine)
}

// dropDatabase removes a database and its user. Connections to it are
// closed first.
func dropDatabase(ctx context.Context, in Instance, dir, rootPassword, name, user string) error {
	if err := checkNames(name, user); err != nil {
		return err
	}
	db, err := connect(in, dir, rootPassword)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	switch in.Engine {
	case MariaDB, MySQL:
		return exec(ctx, db, "DROP DATABASE IF EXISTS `"+name+"`", "DROP USER IF EXISTS '"+user+"'@'%'")
	case Postgres:
		return exec(ctx, db, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`, `DROP ROLE IF EXISTS "`+user+`"`)
	}
	return fmt.Errorf("unknown engine %q", in.Engine)
}

// setPassword changes a database user's password.
func setPassword(ctx context.Context, in Instance, dir, rootPassword, user, pass string) error {
	if err := checkNames(user); err != nil {
		return err
	}
	if err := checkPassword(pass); err != nil {
		return err
	}
	db, err := connect(in, dir, rootPassword)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if in.Engine == Postgres {
		return exec(ctx, db, `ALTER ROLE "`+user+`" PASSWORD '`+pass+`'`)
	}
	return exec(ctx, db, "ALTER USER '"+user+"'@'%' IDENTIFIED BY '"+pass+"'")
}

// ping checks an instance answers.
func ping(ctx context.Context, in Instance, dir, rootPassword string) error {
	db, err := connect(in, dir, rootPassword)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.PingContext(ctx)
}

func exec(ctx context.Context, db *sql.DB, stmts ...string) error {
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}
