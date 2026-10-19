package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/databases"
)

// Database instances on the node and servers' databases on them
// (docs/DATABASES.md).
const (
	NodeDBInstances      = "node.db.instances" // a read: the instances, and the versions offered
	NodeDBInstanceSave   = "node.db.instance.save"
	NodeDBInstancePower  = "node.db.instance.power"
	NodeDBInstanceDelete = "node.db.instance.delete"

	DBList          = "db.list" // a read: a server's databases, and the instances it can use
	DBCreate        = "db.create"
	DBDelete        = "db.delete"
	DBPassword      = "db.password" // show a database's password
	DBPasswordReset = "db.password.reset"
)

// DBInstancesResult is node.db.instances' result.
type DBInstancesResult struct {
	Instances []databases.Instance `json:"instances"`
	Versions  []databases.Version  `json:"versions"`
}

// DBInstanceParams are node.db.instance.delete's params, and with Action
// node.db.instance.power's.
type DBInstanceParams struct {
	ID     string `json:"id"`
	Action string `json:"action,omitempty"` // start, stop, or restart
}

// DBListResult is db.list's result.
type DBListResult struct {
	Databases []databases.Database `json:"databases"`
	// Instances are the ones the server can have databases on.
	Instances []databases.Instance `json:"instances"`
}

// DBCreateParams are db.create's params.
type DBCreateParams struct {
	Instance string `json:"instance"`
	Label    string `json:"label"`
}

// DBParams are db.delete's, db.password's, and db.password.reset's params.
type DBParams struct {
	ID string `json:"id"`
}

// DBPasswordResult is a database's password, with the database when it
// was just made.
type DBPasswordResult struct {
	Database *databases.Database `json:"database,omitempty"`
	Password string              `json:"password"`
}

// RegisterDatabases adds the database actions. Deleting an instance or a
// database, which deletes its data, and showing a password are signed by
// a passkey; the rest change no data.
func RegisterDatabases(x *command.Executor, d *databases.Service) {
	needServer := func(e command.Envelope) error {
		if e.ServerID == "" {
			return errors.New("command needs a server_id")
		}
		return nil
	}
	x.Register(NodeDBInstances, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, _ command.Envelope) (any, error) {
		list, err := d.List(ctx)
		if err != nil {
			return nil, err
		}
		return DBInstancesResult{Instances: list, Versions: databases.Versions}, nil
	}})
	x.Register(NodeDBInstanceSave, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p databases.InstanceInput
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return d.Save(ctx, p)
	}})
	x.Register(NodeDBInstancePower, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBInstanceParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.Power(ctx, p.ID, p.Action)
	}})
	x.Register(NodeDBInstanceDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBInstanceParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.Delete(ctx, p.ID)
	}})

	x.Register(DBList, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		dbs, err := d.ServerDatabases(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		usable, err := d.Usable(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		return DBListResult{Databases: dbs, Instances: usable}, nil
	}})
	x.Register(DBCreate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		var p DBCreateParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		db, pass, err := d.CreateDatabase(ctx, e.ServerID, p.Instance, p.Label)
		if err != nil {
			return nil, err
		}
		return DBPasswordResult{Database: &db, Password: pass}, nil
	}})
	x.Register(DBDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.DeleteDatabase(ctx, e.ServerID, p.ID)
	}})
	// Showing a password is signed, so a stolen session alone can't read
	// one; it changes nothing, but its result isn't stored either.
	x.Register(DBPassword, command.Handler{Signed: command.Always, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		pass, err := d.Password(ctx, e.ServerID, p.ID)
		if err != nil {
			return nil, err
		}
		return DBPasswordResult{Password: pass}, nil
	}})
	x.Register(DBPasswordReset, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		pass, err := d.ResetPassword(ctx, e.ServerID, p.ID)
		if err != nil {
			return nil, err
		}
		return DBPasswordResult{Password: pass}, nil
	}})
}
