package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// An instance's own page (docs/DATABASES.md#the-browser, DECISIONS #330):
// admins see the instance as a whole as its administrator (its databases,
// whichever server has them; its users and what each can reach; every
// connection to it, and stopping one), and open each database with the
// server's own browser, as that database's user. Nothing from this page
// runs SQL as the administrator: that would let an unsigned command make
// users or, on PostgreSQL, run programs in the instance's container.

// InstanceDatabases returns the databases on an instance, with the
// servers using each.
func (s *Service) InstanceDatabases(ctx context.Context, id string) ([]Database, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: no instance given", ErrNotFound)
	}
	return s.Databases(ctx, id)
}

// adminDB opens an admin connection to a running SQL instance.
func (s *Service) adminDB(ctx context.Context, id string) (*sql.DB, Instance, error) {
	in, ir, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return nil, in, err
	}
	if !isSQL(in.Engine) {
		return nil, in, invalid{fmt.Sprintf("%s instances are seen through each server's database", engineName(in.Engine))}
	}
	if s.state(id) != StateRunning {
		return nil, in, notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return nil, in, err
	}
	db, err := connect(in, s.dir(id), root)
	return db, in, err
}

// InstanceUser is one of an instance's users, and what it can reach.
type InstanceUser struct {
	Name string `json:"name"`
	// Kind: admin (the instance's administrator, Wings' own), database (a
	// database's own user), readonly (its read-only user), or other (made
	// with SQL, or the engine's own).
	Kind       string   `json:"kind"`
	ServerID   string   `json:"server_id,omitempty"`
	DatabaseID string   `json:"database_id,omitempty"`
	Reaches    []string `json:"reaches"`         // the databases it can use; "*" is all of them
	Hosts      []string `json:"hosts,omitempty"` // MariaDB and MySQL: where it may log in from
}

// InstanceUsers lists an instance's users and what each can reach. The
// browser's readers (viewer.go) aren't listed: they're Wings' own.
func (s *Service) InstanceUsers(ctx context.Context, id string) ([]InstanceUser, error) {
	db, in, err := s.adminDB(ctx, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	dbs, err := s.instanceDatabases(ctx, id)
	if err != nil {
		return nil, err
	}
	links, err := s.links(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]InstanceUser{}
	hidden := map[string]bool{}
	for _, d := range dbs {
		// The server is the first one using it, if any.
		var server string
		if l := links[d.ID]; len(l) > 0 {
			server = l[0].ServerID
		}
		known[d.Username] = InstanceUser{Kind: "database", ServerID: server, DatabaseID: d.ID}
		if d.RoUsername != "" {
			known[d.RoUsername] = InstanceUser{Kind: "readonly", ServerID: server, DatabaseID: d.ID}
		}
		if d.ViewerUsername != "" {
			hidden[d.ViewerUsername] = true
		}
	}
	byName := map[string]*InstanceUser{}
	var order []string
	add := func(name string) *InstanceUser {
		if u, ok := byName[name]; ok {
			return u
		}
		u := known[name]
		u.Name = name
		if u.Kind == "" {
			u.Kind = "other"
		}
		u.Reaches = []string{}
		byName[name] = &u
		order = append(order, name)
		return &u
	}
	if in.Engine == Postgres {
		rows, err := db.QueryContext(ctx, `SELECT r.rolname, r.rolsuper,
  array_to_json(ARRAY(SELECT d.datname FROM pg_database d
    WHERE NOT d.datistemplate AND d.datname <> 'postgres' AND has_database_privilege(r.oid, d.oid, 'CONNECT')
    ORDER BY d.datname))::text
FROM pg_roles r WHERE r.rolcanlogin ORDER BY r.rolname`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name, reach string
			var super bool
			if err := rows.Scan(&name, &super, &reach); err != nil {
				return nil, err
			}
			if hidden[name] {
				continue
			}
			u := add(name)
			if super {
				u.Reaches = []string{"*"}
				if name == "postgres" {
					u.Kind = "admin"
				}
				continue
			}
			_ = json.Unmarshal([]byte(reach), &u.Reaches)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		rows, err := db.QueryContext(ctx, `SELECT u.User, u.Host, u.Select_priv = 'Y', COALESCE(d.Db, '')
FROM mysql.user u LEFT JOIN mysql.db d ON d.User = u.User AND d.Host = u.Host
WHERE u.User NOT IN ('', 'mysql.sys', 'mysql.session', 'mysql.infoschema', 'mariadb.sys') ORDER BY u.User, u.Host, d.Db`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name, host, database string
			var all bool
			if err := rows.Scan(&name, &host, &all, &database); err != nil {
				return nil, err
			}
			if hidden[name] {
				continue
			}
			u := add(name)
			if !slices.Contains(u.Hosts, host) {
				u.Hosts = append(u.Hosts, host)
			}
			switch {
			case slices.Contains(u.Reaches, "*"):
			case all:
				u.Reaches = []string{"*"}
				if name == "root" {
					u.Kind = "admin"
				}
			case database != "":
				// GRANT … ON `a\_b`.* is stored escaped.
				database = strings.ReplaceAll(database, `\_`, "_")
				if !slices.Contains(u.Reaches, database) {
					u.Reaches = append(u.Reaches, database)
				}
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]InstanceUser, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// InstanceProcesses lists every connection to an instance, with the
// database it's using and its running query.
func (s *Service) InstanceProcesses(ctx context.Context, id string) ([]Process, error) {
	db, in, err := s.adminDB(ctx, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	q := `SELECT ID, USER, COALESCE(DB, ''), COALESCE(STATE, COMMAND), TIME, COALESCE(INFO, '') FROM information_schema.PROCESSLIST
WHERE ID <> CONNECTION_ID() AND USER NOT IN ('system user', 'event_scheduler') AND COMMAND <> 'Daemon' ORDER BY TIME DESC`
	if in.Engine == Postgres {
		q = `SELECT pid, COALESCE(usename, ''), COALESCE(datname, ''), COALESCE(state, ''),
  COALESCE(EXTRACT(EPOCH FROM now() - query_start)::bigint, 0), COALESCE(query, '')
FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid() ORDER BY query_start NULLS LAST`
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Process{}
	for rows.Next() {
		var p Process
		if err := rows.Scan(&p.ID, &p.User, &p.Database, &p.State, &p.Seconds, &p.Query); err != nil {
			return nil, err
		}
		if len(p.Query) > maxCell {
			p.Query = strings.ToValidUTF8(p.Query[:maxCell], "")
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// KillInstanceQuery stops a query running on an instance, whoever's it is.
func (s *Service) KillInstanceQuery(ctx context.Context, id string, process int64) error {
	db, in, err := s.adminDB(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if in.Engine == Postgres {
		var ok bool
		if err := db.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", process).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalid{"that query isn't running any more"}
		}
		return nil
	}
	return exec(ctx, db, "KILL QUERY "+strconv.FormatInt(process, 10))
}
