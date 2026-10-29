package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/databases"
)

// A database instance's own backups (docs/DATABASES.md#backups). Admins
// only, like the rest of an instance (the Panel's permissions).
const (
	NodeDBBackups        = "node.db.backups" // a read: an instance's backups and settings
	NodeDBBackupCreate   = "node.db.backup.create"
	NodeDBBackupDelete   = "node.db.backup.delete"
	NodeDBBackupRestore  = "node.db.backup.restore"
	NodeDBBackupSettings = "node.db.backup.settings"
)

// DBBackupsParams are node.db.backups' and node.db.backup.create's params.
type DBBackupsParams struct {
	InstanceID string `json:"instance_id"`
}

// DBBackupsResult is node.db.backups' result.
type DBBackupsResult struct {
	Backups  []*backup.Backup      `json:"backups"`
	Settings backup.InstancePolicy `json:"settings"`
	// Destinations are where its backups can go: the node's, without
	// recovered (read-only) ones.
	Destinations []DBBackupDestination `json:"destinations"`
}

// DBBackupDestination is a destination an instance's backups can go to.
type DBBackupDestination struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// DBBackupParams are node.db.backup.delete's params, and with DatabaseID
// node.db.backup.restore's.
type DBBackupParams struct {
	InstanceID string `json:"instance_id"`
	ID         string `json:"id"`
	DatabaseID string `json:"database_id,omitempty"`
}

// DBBackupSettingsParams are node.db.backup.settings' params.
type DBBackupSettingsParams struct {
	InstanceID string `json:"instance_id"`
	backup.InstancePolicy
}

// RegisterDatabaseBackups adds database instances' own backups.
func RegisterDatabaseBackups(x *command.Executor, d *databases.Service, b *backup.Manager) {
	x.Register(NodeDBBackups, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBBackupsParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if p.InstanceID == "" {
			return nil, errors.New("no instance given")
		}
		list, err := b.InstanceBackups(ctx, p.InstanceID)
		if err != nil {
			return nil, err
		}
		pol, err := b.InstancePolicy(ctx, p.InstanceID)
		if err != nil {
			return nil, err
		}
		dests, err := b.Destinations(ctx)
		if err != nil {
			return nil, err
		}
		out := DBBackupsResult{Backups: list, Settings: pol, Destinations: []DBBackupDestination{}}
		for _, dst := range dests {
			if !dst.ReadOnly {
				out.Destinations = append(out.Destinations, DBBackupDestination{ID: dst.ID, Name: dst.Name, Type: dst.Type})
			}
		}
		if out.Backups == nil {
			out.Backups = []*backup.Backup{}
		}
		return out, nil
	}})

	x.Register(NodeDBBackupCreate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBBackupsParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		bk, err := b.BackupInstance(ctx, p.InstanceID, backup.KindManual, e.UserID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"backup_id": bk.ID, "job_id": bk.JobID}, nil
	}})

	// Deleting a backup forgets data, so it's signed like a server's.
	x.Register(NodeDBBackupDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBBackupParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		jobID, err := b.DeleteInstanceBackup(ctx, p.InstanceID, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobID}, nil
	}})

	// Restoring replaces a database's data, so it's signed like db.restore.
	x.Register(NodeDBBackupRestore, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DBBackupParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if p.DatabaseID == "" {
			return nil, errors.New("pick a database to restore")
		}
		jobID, err := d.RestoreFromInstanceBackup(ctx, p.InstanceID, p.DatabaseID, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobID}, nil
	}})

	x.Register(NodeDBBackupSettings, command.Handler{
		// Keeping fewer lets retention delete backups, and a new
		// destination copies the databases off the node (but the node's
		// own disk and Raptor Backup Storage), as for a server's.
		Signed: func(ctx context.Context, e command.Envelope) (bool, error) {
			var p DBBackupSettingsParams
			if err := decode(e, &p); err != nil {
				return false, err
			}
			cur, err := b.InstancePolicy(ctx, p.InstanceID)
			if err != nil {
				return false, err
			}
			moved := p.DestinationID != cur.DestinationID && p.DestinationID != backup.LocalDestination && p.DestinationID != backup.HostedDestination
			return p.KeepsLess(cur) || moved, nil
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			var p DBBackupSettingsParams
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			if err := b.SetInstancePolicy(ctx, p.InstanceID, p.InstancePolicy); err != nil {
				return nil, err
			}
			return b.InstancePolicy(ctx, p.InstanceID)
		},
	})
}
