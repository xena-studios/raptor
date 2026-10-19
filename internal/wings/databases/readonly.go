package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// A database's read-only user (docs/DATABASES.md#the-browser) is for a tool
// that should only look: a stats page, a map, a backup script. It's
// r<the server's short ID>_<name>, beside the database's own s…, and can
// read every table, including ones made later, and change nothing.

func roName(serverID, label string) string {
	return "r" + server.ShortID(serverID) + "_" + label
}

// SetReadOnly makes a database's read-only user, or gives it a new
// password, and returns the password.
func (s *Service) SetReadOnly(ctx context.Context, serverID, id string) (string, string, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", "", err
	}
	defer s.lock(r.InstanceID)()
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return "", "", err
	}
	if s.state(r.InstanceID) != StateRunning {
		return "", "", notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return "", "", err
	}
	user := roName(serverID, r.Label)
	if err := checkNames(user, r.DbName, r.Username); err != nil {
		return "", "", err
	}
	pass, err := password()
	if err != nil {
		return "", "", err
	}
	sealed, err := s.sealer.seal(pass)
	if err != nil {
		return "", "", err
	}
	dir := s.dir(r.InstanceID)
	db, err := connect(in, dir, root)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = db.Close() }()
	switch in.Engine {
	case MariaDB, MySQL:
		err = exec(ctx, db,
			"CREATE USER IF NOT EXISTS '"+user+"'@'%' IDENTIFIED BY '"+pass+"'",
			"ALTER USER '"+user+"'@'%' IDENTIFIED BY '"+pass+"'",
			"GRANT SELECT, SHOW VIEW ON `"+r.DbName+"`.* TO '"+user+"'@'%'",
		)
	case Postgres:
		var exists bool
		if err = db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '"+user+"')").Scan(&exists); err != nil {
			break
		}
		role := `CREATE ROLE "` + user + `" LOGIN PASSWORD '` + pass + `'`
		if exists {
			role = `ALTER ROLE "` + user + `" LOGIN PASSWORD '` + pass + `'`
		}
		if err = exec(ctx, db, role, `GRANT CONNECT ON DATABASE "`+r.DbName+`" TO "`+user+`"`); err != nil {
			break
		}
		// Every schema the database's user has, now and later: SELECT on
		// its tables, and on the tables it makes from now on.
		inner := connectTo(dir, root, r.DbName)
		defer func() { _ = inner.Close() }()
		err = exec(ctx, inner, `DO $$
DECLARE s name;
BEGIN
  FOR s IN SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_%' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, '`+user+`');
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA %I TO %I', s, '`+user+`');
    EXECUTE format('GRANT SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, '`+user+`');
  END LOOP;
END $$`,
			`ALTER DEFAULT PRIVILEGES FOR ROLE "`+r.Username+`" GRANT SELECT ON TABLES TO "`+user+`"`,
			`ALTER DEFAULT PRIVILEGES FOR ROLE "`+r.Username+`" GRANT SELECT ON SEQUENCES TO "`+user+`"`,
			`ALTER DEFAULT PRIVILEGES FOR ROLE "`+r.Username+`" GRANT USAGE ON SCHEMAS TO "`+user+`"`,
		)
	}
	if err != nil {
		return "", "", fmt.Errorf("making the read-only user on %s: %w", in.Name, err)
	}
	if err := s.o.Store.Write.SetDatabaseReadOnly(ctx, store.SetDatabaseReadOnlyParams{RoUsername: user, RoPassword: sealed, ID: id}); err != nil {
		return "", "", err
	}
	if in.Engine == Postgres {
		if err := s.reloadHBA(ctx, in, dir, root); err != nil {
			return "", "", err
		}
	}
	return user, pass, nil
}

// reloadHBA writes pg_hba.conf again and has PostgreSQL read it.
func (s *Service) reloadHBA(ctx context.Context, in Instance, dir, root string) error {
	if err := s.writeHBA(ctx, in.ID, dir); err != nil {
		return err
	}
	db, err := connect(in, dir, root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return exec(ctx, db, "SELECT pg_reload_conf()")
}

// RemoveReadOnly drops a database's read-only user.
func (s *Service) RemoveReadOnly(ctx context.Context, serverID, id string) error {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return err
	}
	if r.RoUsername == "" {
		return errors.New("the database has no read-only user")
	}
	defer s.lock(r.InstanceID)()
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return err
	}
	if s.state(r.InstanceID) != StateRunning {
		return notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return err
	}
	if err := dropReadOnly(ctx, in, s.dir(r.InstanceID), root, r.DbName, r.RoUsername); err != nil {
		return err
	}
	if err := s.o.Store.Write.SetDatabaseReadOnly(ctx, store.SetDatabaseReadOnlyParams{ID: id}); err != nil {
		return err
	}
	if in.Engine == Postgres {
		return s.reloadHBA(ctx, in, s.dir(r.InstanceID), root)
	}
	return nil
}

// dropReadOnly drops a read-only user, with what it was granted.
func dropReadOnly(ctx context.Context, in Instance, dir, root, database, user string) error {
	if err := checkNames(database, user); err != nil {
		return err
	}
	db, err := connect(in, dir, root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if in.Engine != Postgres {
		return exec(ctx, db, "DROP USER IF EXISTS '"+user+"'@'%'")
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = '"+database+"')").Scan(&exists); err != nil {
		return err
	}
	if exists {
		inner := connectTo(dir, root, database)
		defer func() { _ = inner.Close() }()
		if err := exec(ctx, inner, `DROP OWNED BY "`+user+`"`); err != nil {
			return err
		}
	}
	return exec(ctx, db, `DROP ROLE IF EXISTS "`+user+`"`)
}

// ReadOnlyPassword returns a database's read-only user's password.
func (s *Service) ReadOnlyPassword(ctx context.Context, serverID, id string) (string, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	if r.RoUsername == "" || r.RoPassword == nil {
		return "", errors.New("the database has no read-only user")
	}
	return s.sealer.open(r.RoPassword)
}
