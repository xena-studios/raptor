package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// The browser's reader (docs/DATABASES.md#permissions, DECISIONS #329):
// those who may only look at a SQL database run their SQL as a read-only
// user Wings makes for it, v<the server's short ID>_<name>, beside the
// database's own s… and the owner's r…. It's made the first time it's
// needed, can read every table (including ones made later) and change
// nothing, and logs in only through the instance's socket, which only
// Wings reaches: MariaDB and MySQL's account is 'v…'@'localhost', and
// PostgreSQL's pg_hba.conf lets it in over the network nowhere (its name
// isn't its database's). Owners never see it or its password.

func viewerName(serverID, label string) string {
	return "v" + server.ShortID(serverID) + "_" + label
}

// openViewer connects to one of a server's databases as its reader.
func (s *Service) openViewer(ctx context.Context, serverID, id string) (*session, error) {
	r, in, root, err := s.browsable(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	dir := s.dir(r.InstanceID)
	user, pass, err := s.viewer(ctx, r, in, root, false)
	if err != nil {
		return nil, err
	}
	se, err := connectAs(ctx, in, dir, root, user, pass, r.DbName, false)
	if loginRefused(err) {
		// Gone or changed behind Wings' back (a restore or version change
		// that didn't carry it): make it again, once.
		if user, pass, err = s.viewer(ctx, r, in, root, true); err != nil {
			return nil, err
		}
		se, err = connectAs(ctx, in, dir, root, user, pass, r.DbName, false)
	}
	return se, err
}

// viewer returns a database's reader and its password, making it first
// if it isn't there yet, or again when again is set.
func (s *Service) viewer(ctx context.Context, r store.Database, in Instance, root string, again bool) (string, string, error) {
	if r.ViewerUsername != "" && r.ViewerPassword != nil && !again {
		pass, err := s.sealer.open(r.ViewerPassword)
		return r.ViewerUsername, pass, err
	}
	defer s.lock(r.InstanceID)()
	// Another reader may have made it while this one waited.
	if cur, err := s.o.Store.Read.GetDatabase(ctx, r.ID); err == nil && cur.ViewerUsername != "" && cur.ViewerPassword != nil && !again {
		pass, err := s.sealer.open(cur.ViewerPassword)
		return cur.ViewerUsername, pass, err
	}
	user := viewerName(r.ServerID, r.Label)
	pass, err := password()
	if err != nil {
		return "", "", err
	}
	sealed, err := s.sealer.seal(pass)
	if err != nil {
		return "", "", err
	}
	if err := grantReader(ctx, in, s.dir(r.InstanceID), root, r.DbName, r.Username, user, pass, "localhost"); err != nil {
		return "", "", fmt.Errorf("making the database's reader on %s: %w", in.Name, err)
	}
	if err := s.o.Store.Write.SetDatabaseViewer(ctx, store.SetDatabaseViewerParams{ViewerUsername: user, ViewerPassword: sealed, ID: r.ID}); err != nil {
		return "", "", err
	}
	return user, pass, nil
}

// regrantViewer gives a database's reader its rights again, after its
// database was made again (a restore, a version change by migration).
func (s *Service) regrantViewer(ctx context.Context, in Instance, r store.Database, root string) error {
	if r.ViewerUsername == "" || r.ViewerPassword == nil || !isSQL(in.Engine) {
		return nil
	}
	pass, err := s.sealer.open(r.ViewerPassword)
	if err != nil {
		return err
	}
	return grantReader(ctx, in, s.dir(in.ID), root, r.DbName, r.Username, r.ViewerUsername, pass, "localhost")
}

// loginRefused reports whether a connection failed because the engine
// refused the user or its password.
func loginRefused(err error) bool {
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1045 || my.Number == 1044 // access denied (to the database)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "28P01" || pg.Code == "28000" || pg.Code == "42501" // bad password, not allowed, no CONNECT
	}
	return false
}
