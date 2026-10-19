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
	// ReadOnly: db.password shows the read-only user's password.
	ReadOnly bool `json:"readonly,omitempty"`
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
		get := d.Password
		if p.ReadOnly {
			get = d.ReadOnlyPassword
		}
		pass, err := get(ctx, e.ServerID, p.ID)
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

// The browser's actions (docs/DATABASES.md#the-browser): all on one of the
// server's databases, as its own user.
const (
	DBSchema      = "db.schema"     // a read: tables, columns, indexes
	DBRows        = "db.rows"       // a read: a page of a table's rows
	DBQueryRead   = "db.query.read" // one statement in a read-only transaction
	DBQuery       = "db.query"      // any SQL
	DBRowInsert   = "db.row.insert"
	DBRowUpdate   = "db.row.update"
	DBRowDelete   = "db.row.delete"
	DBProcesses   = "db.processes" // a read: connections to the database
	DBProcessKill = "db.process.kill"
)

// DBQueryParams are db.query's and db.query.read's params.
type DBQueryParams struct {
	ID string `json:"id"`
	databases.QueryInput
}

// DBRowsParams are db.rows' params.
type DBRowsParams struct {
	ID string `json:"id"`
	databases.RowsQuery
}

// DBRowParams are db.row.insert's, db.row.update's, and db.row.delete's
// params.
type DBRowParams struct {
	ID string `json:"id"`
	databases.RowEdit
}

// DBKillParams are db.process.kill's params.
type DBKillParams struct {
	ID      string `json:"id"`
	Process int64  `json:"process"`
}

// RegisterDatabaseBrowser adds the browser's actions. None is signed: the
// database's own user can't reach past its database, and changes are in
// the activity (without their SQL, which can hold data).
func RegisterDatabaseBrowser(x *command.Executor, d *databases.Service) {
	server := func(run func(ctx context.Context, e command.Envelope) (any, error)) func(ctx context.Context, e command.Envelope) (any, error) {
		return func(ctx context.Context, e command.Envelope) (any, error) {
			if e.ServerID == "" {
				return nil, errors.New("command needs a server_id")
			}
			return run(ctx, e)
		}
	}
	x.Register(DBSchema, command.Handler{Signed: command.Never, ReadOnly: true, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		tables, err := d.Schema(ctx, e.ServerID, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tables": tables}, nil
	})})
	x.Register(DBRows, command.Handler{Signed: command.Never, ReadOnly: true, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBRowsParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return d.Rows(ctx, e.ServerID, p.ID, p.RowsQuery)
	})})
	query := func(readOnly bool) func(ctx context.Context, e command.Envelope) (any, error) {
		return server(func(ctx context.Context, e command.Envelope) (any, error) {
			var p DBQueryParams
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			return d.Query(ctx, e.ServerID, p.ID, p.QueryInput, readOnly)
		})
	}
	x.Register(DBQueryRead, command.Handler{Signed: command.Never, ReadOnly: true, Run: query(true)})
	x.Register(DBQuery, command.Handler{Signed: command.Never, Run: query(false)})
	edit := func(op string) command.Handler {
		return command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
			var p DBRowParams
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			n, err := d.EditRows(ctx, e.ServerID, p.ID, op, p.RowEdit)
			if err != nil {
				return nil, err
			}
			return map[string]int64{"affected": n}, nil
		})}
	}
	x.Register(DBRowInsert, edit("insert"))
	x.Register(DBRowUpdate, edit("update"))
	x.Register(DBRowDelete, edit("delete"))
	x.Register(DBProcesses, command.Handler{Signed: command.Never, ReadOnly: true, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		list, err := d.Processes(ctx, e.ServerID, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"processes": list}, nil
	})})
	x.Register(DBProcessKill, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBKillParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.KillQuery(ctx, e.ServerID, p.ID, p.Process)
	})})
}

// Imports, exports, and read-only users (docs/DATABASES.md#the-browser).
const (
	DBImport         = "db.import"          // run a .sql file of the server's
	DBExport         = "db.export"          // dump to the server's database-exports
	DBTransferStatus = "db.transfer.status" // a read
	DBTransferCancel = "db.transfer.cancel"
	DBReadOnlySet    = "db.readonly.set" // make the read-only user, or give it a new password
	DBReadOnlyRemove = "db.readonly.remove"
)

// DBImportParams are db.import's params.
type DBImportParams struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// DBExportParams are db.export's params.
type DBExportParams struct {
	ID     string `json:"id"`
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table,omitempty"`
	Format string `json:"format"` // sql or csv
}

// DBJobParams are db.transfer.status's and db.transfer.cancel's params.
type DBJobParams struct {
	JobID string `json:"job_id"`
}

// RegisterDatabaseTransfers adds imports, exports, and read-only users.
// None is signed: an import changes data like any SQL does, and an export
// writes a file the server could have made itself.
func RegisterDatabaseTransfers(x *command.Executor, d *databases.Service) {
	server := func(run func(ctx context.Context, e command.Envelope) (any, error)) func(ctx context.Context, e command.Envelope) (any, error) {
		return func(ctx context.Context, e command.Envelope) (any, error) {
			if e.ServerID == "" {
				return nil, errors.New("command needs a server_id")
			}
			return run(ctx, e)
		}
	}
	x.Register(DBImport, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBImportParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, err := d.Import(ctx, e.ServerID, p.ID, p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id}, nil
	})})
	x.Register(DBExport, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBExportParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, path, err := d.Export(ctx, e.ServerID, p.ID, p.Schema, p.Table, p.Format)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id, "path": path}, nil
	})})
	x.Register(DBTransferStatus, command.Handler{Signed: command.Never, ReadOnly: true, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBJobParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return d.TransferStatus(ctx, e.ServerID, p.JobID)
	})})
	x.Register(DBTransferCancel, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBJobParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.CancelTransfer(ctx, e.ServerID, p.JobID)
	})})
	x.Register(DBReadOnlySet, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		user, pass, err := d.SetReadOnly(ctx, e.ServerID, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"user": user, "password": pass}, nil
	})})
	x.Register(DBReadOnlyRemove, command.Handler{Signed: command.Never, Run: server(func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, d.RemoveReadOnly(ctx, e.ServerID, p.ID)
	})})
}

// Versions and backups (docs/DATABASES.md#changing-an-instances-version).
const (
	NodeDBInstanceVersion = "node.db.instance.version" // change its version, or update its patch
	NodeDBInstanceGoBack  = "node.db.instance.goback"  // back to the version and data from before
	DBRestore             = "db.restore"               // one database from one of the server's backups
)

// DBVersionParams are node.db.instance.version's params.
type DBVersionParams struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"` // "" is the newest patch of its own
}

// DBRestoreParams are db.restore's params.
type DBRestoreParams struct {
	ID       string `json:"id"`
	BackupID string `json:"backup_id"`
}

// RegisterDatabaseVersions adds version changes and restoring a database
// from a backup. Going back and restoring replace what a database has now,
// so they're signed; a version change puts itself back if anything's off.
func RegisterDatabaseVersions(x *command.Executor, d *databases.Service) {
	x.Register(NodeDBInstanceVersion, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBVersionParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, err := d.ChangeVersion(ctx, p.ID, p.Version)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id}, nil
	}})
	x.Register(NodeDBInstanceGoBack, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBInstanceParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, err := d.GoBack(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id}, nil
	}})
	x.Register(DBRestore, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		var p DBRestoreParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, before, err := d.RestoreDatabase(ctx, e.ServerID, p.ID, p.BackupID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id, "before": before}, nil
	}})
}
