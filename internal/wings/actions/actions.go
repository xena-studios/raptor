// Package actions maps commands from the Panel to the server manager, and
// says which of them must be signed with the user's passkey
// (docs/SECURITY-MODEL.md#passkey-signed-commands).
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/sftp"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Action names.
const (
	ServerCreate    = "server.create"
	ServerUpdate    = "server.update"
	ServerDelete    = "server.delete"
	ServerReinstall = "server.reinstall"
	ServerStart     = "server.start"
	ServerStop      = "server.stop"
	ServerRestart   = "server.restart"
	ServerKill      = "server.kill"
	ServerCommand   = "server.command"
	// ServerConsole watches a server's console (NodeService.Console). It
	// isn't a command: the grant is checked, then lines stream until the
	// Panel hangs up.
	ServerConsole = "server.console"

	ScheduleCreate = "schedule.create"
	ScheduleUpdate = "schedule.update"
	ScheduleDelete = "schedule.delete"
	ScheduleRun    = "schedule.run" // run now

	BackupCreate            = "backup.create"
	BackupRestore           = "backup.restore"
	BackupDelete            = "backup.delete"
	BackupLock              = "backup.lock"
	BackupPolicy            = "backup.policy.update"
	BackupDestinationSave   = "backup.destination.save" // create or update
	BackupDestinationDelete = "backup.destination.delete"

	NodeSFTP = "node.sftp" // turn SFTP on or off

	FilesList         = "files.list"
	FilesStat         = "files.stat"
	FilesRead         = "files.read"
	FilesWrite        = "files.write"
	FilesMkdir        = "files.mkdir"
	FilesRename       = "files.rename"
	FilesCopy         = "files.copy"
	FilesDelete       = "files.delete"
	FilesChmod        = "files.chmod"
	FilesCompress     = "files.compress"
	FilesDecompress   = "files.decompress"
	FilesUpload       = "files.upload" // start an upload; chunks go over a transfer connection
	FilesUploadStatus = "files.upload.status"
	FilesUploadCancel = "files.upload.cancel"
	FilesDownload     = "files.download" // start a download
)

// ServerConfig is a server's configuration as sent by the Panel.
type ServerConfig struct {
	Name        string              `json:"name"`
	Egg         []byte              `json:"egg"` // the egg file (JSON or YAML)
	EggSource   string              `json:"egg_source,omitempty"`
	Image       string              `json:"image,omitempty"`
	Startup     string              `json:"startup,omitempty"`
	Variables   map[string]string   `json:"variables,omitempty"`
	Limits      containers.Limits   `json:"limits"`
	Settings    *server.Settings    `json:"settings,omitempty"`
	HostNetwork bool                `json:"host_network,omitempty"`
	Allocations []server.Allocation `json:"allocations"`
}

func (c ServerConfig) toConfig() server.Config {
	settings := server.DefaultSettings()
	if c.Settings != nil {
		settings = *c.Settings
	}
	return server.Config{
		Name: c.Name, Egg: c.Egg, EggSource: c.EggSource, Image: c.Image, Startup: c.Startup,
		Variables: c.Variables, Limits: c.Limits, Settings: settings, HostNetwork: c.HostNetwork,
		Allocations: c.Allocations,
	}
}

// CreateParams are the params of server.create.
type CreateParams struct {
	ServerConfig
	StartAfterInstall bool `json:"start_after_install,omitempty"`
	// AcceptEULA: the user accepted the game's EULA (eggs with the "eula"
	// feature); Wings writes eula.txt after the install. Part of what the
	// user's passkey signs.
	AcceptEULA bool `json:"accept_eula,omitempty"`
	// BackupSchedule: add the default daily backup schedule (default true).
	BackupSchedule *bool `json:"backup_schedule,omitempty"`
}

// Defaults are what new servers get.
type Defaults struct {
	// BackupSchedule adds the daily backup schedule to a new server, in the
	// transaction that creates it. nil = none.
	BackupSchedule func(ctx context.Context, q *store.Queries, serverID string) error
}

// DeleteParams are the params of server.delete.
type DeleteParams struct {
	// FinalBackup: back the server up to its backup destination first,
	// and keep it if that fails. The deletion then runs as a job.
	FinalBackup bool `json:"final_backup,omitempty"`
}

// ReinstallParams are the params of server.reinstall.
type ReinstallParams struct {
	// Wipe: remove every file (after a safety backup) before the install
	// script runs.
	Wipe bool `json:"wipe,omitempty"`
}

// CommandParams are the params of server.command.
type CommandParams struct {
	Command string `json:"command"`
}

// Register adds every server action to the executor.
func Register(x *command.Executor, m *server.Manager, d Defaults) {
	needServer := func(e command.Envelope) error {
		if e.ServerID == "" {
			return errors.New("command needs a server_id")
		}
		return nil
	}
	power := func(a server.PowerAction) command.Handler {
		return command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
			if err := needServer(e); err != nil {
				return nil, err
			}
			return nil, m.Power(ctx, e.ServerID, a, e.UserID)
		}}
	}

	// Creating a server chooses an egg, which is choosing what code runs:
	// always signed.
	x.Register(ServerCreate, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p CreateParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		opts := server.CreateOptions{StartAfterInstall: p.StartAfterInstall, AcceptEULA: p.AcceptEULA}
		if d.BackupSchedule != nil && (p.BackupSchedule == nil || *p.BackupSchedule) {
			opts.InTx = d.BackupSchedule
		}
		id, err := m.Create(ctx, p.toConfig(), opts)
		if err != nil {
			return nil, err
		}
		return map[string]string{"server_id": id}, nil
	}})

	// An update is signed when it changes the egg, image, or startup command.
	x.Register(ServerUpdate, command.Handler{
		Signed: func(ctx context.Context, e command.Envelope) (bool, error) {
			var p ServerConfig
			if err := decode(e, &p); err != nil {
				return false, err
			}
			if err := needServer(e); err != nil {
				return false, err
			}
			return m.ChangesCode(ctx, e.ServerID, p.toConfig())
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			var p ServerConfig
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			return nil, m.Update(ctx, e.ServerID, p.toConfig())
		},
	})

	x.Register(ServerDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		var p DeleteParams
		if err := decodeOptional(e, &p); err != nil {
			return nil, err
		}
		if !p.FinalBackup {
			return nil, m.Delete(ctx, e.ServerID)
		}
		job, err := m.DeleteWithBackup(ctx, e.ServerID, e.UserID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": job}, nil
	}})

	// A plain reinstall re-runs the egg the owner already approved. "Wipe
	// and reinstall" deletes every file (after a safety backup), so it's
	// signed.
	x.Register(ServerReinstall, command.Handler{
		Signed: func(_ context.Context, e command.Envelope) (bool, error) {
			var p ReinstallParams
			err := decodeOptional(e, &p)
			return p.Wipe, err
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			if err := needServer(e); err != nil {
				return nil, err
			}
			var p ReinstallParams
			if err := decodeOptional(e, &p); err != nil {
				return nil, err
			}
			job, err := m.Reinstall(ctx, e.ServerID, server.InstallOptions{Wipe: p.Wipe})
			if err != nil {
				return nil, err
			}
			return map[string]string{"job_id": job}, nil
		},
	})

	x.Register(ServerStart, power(server.PowerStart))
	x.Register(ServerStop, power(server.PowerStop))
	x.Register(ServerRestart, power(server.PowerRestart))
	x.Register(ServerKill, power(server.PowerKill))

	x.Register(ServerCommand, command.Handler{Signed: command.Never, Run: func(_ context.Context, e command.Envelope) (any, error) {
		var p CommandParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if err := needServer(e); err != nil {
			return nil, err
		}
		return nil, m.SendCommand(e.ServerID, e.UserID, p.Command)
	}})
}

// ScheduleParams are the params of the schedule actions. ScheduleID is
// empty for schedule.create; the definition is only read by create and
// update.
type ScheduleParams struct {
	ScheduleID string `json:"schedule_id,omitempty"`
	schedule.Definition
}

// RegisterSchedules adds the schedule actions. None are signed: a schedule
// only does what the user could already do unsigned (console commands and
// power actions) (docs/SECURITY-MODEL.md#passkey-signed-commands).
func RegisterSchedules(x *command.Executor, s *schedule.Scheduler) {
	params := func(e command.Envelope, needID bool) (ScheduleParams, error) {
		var p ScheduleParams
		if e.ServerID == "" {
			return p, errors.New("command needs a server_id")
		}
		if err := decode(e, &p); err != nil {
			return p, err
		}
		if needID && p.ScheduleID == "" {
			return p, errors.New("command needs a schedule_id")
		}
		return p, nil
	}
	result := func(sc *schedule.Schedule) map[string]any {
		r := map[string]any{"schedule_id": sc.ID}
		if !sc.NextRun.IsZero() {
			r["next_run_at"] = sc.NextRun.UnixMilli()
		}
		return r
	}

	x.Register(ScheduleCreate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := params(e, false)
		if err != nil {
			return nil, err
		}
		sc, err := s.Create(ctx, e.ServerID, p.Definition)
		if err != nil {
			return nil, err
		}
		return result(sc), nil
	}})
	x.Register(ScheduleUpdate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := params(e, true)
		if err != nil {
			return nil, err
		}
		sc, err := s.Update(ctx, e.ServerID, p.ScheduleID, p.Definition)
		if err != nil {
			return nil, err
		}
		return result(sc), nil
	}})
	x.Register(ScheduleDelete, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := params(e, true)
		if err != nil {
			return nil, err
		}
		return nil, s.Delete(ctx, e.ServerID, p.ScheduleID)
	}})
	x.Register(ScheduleRun, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := params(e, true)
		if err != nil {
			return nil, err
		}
		job, err := s.RunNow(ctx, e.ServerID, p.ScheduleID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": job}, nil
	}})
}

// BackupParams are the params of the backup actions that name a backup.
type BackupParams struct {
	BackupID string `json:"backup_id"`
	Locked   bool   `json:"locked,omitempty"` // backup.create, backup.lock
}

// DestinationParams are the params of the destination actions.
type DestinationParams struct {
	backup.Destination
}

// RegisterBackups adds the backup actions. Restoring over a server's files
// and deleting a backup destroy data, so they're signed; so are changes
// that let retention delete more (lower keep values, unlocking a backup),
// which would otherwise delete backups one step removed
// (docs/SECURITY-MODEL.md#passkey-signed-commands).
func RegisterBackups(x *command.Executor, b *backup.Manager) {
	backupParams := func(e command.Envelope, needID bool) (BackupParams, error) {
		var p BackupParams
		if e.ServerID == "" {
			return p, errors.New("command needs a server_id")
		}
		if len(e.Params) > 0 {
			if err := json.Unmarshal(e.Params, &p); err != nil {
				return p, fmt.Errorf("params: %w", err)
			}
		}
		if needID && p.BackupID == "" {
			return p, errors.New("command needs a backup_id")
		}
		return p, nil
	}
	jobResult := func(id string) map[string]string { return map[string]string{"job_id": id} }

	x.Register(BackupCreate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := backupParams(e, false)
		if err != nil {
			return nil, err
		}
		bk, err := b.Create(ctx, e.ServerID, backup.CreateOptions{Kind: backup.KindManual, User: e.UserID, Locked: p.Locked})
		if err != nil {
			return nil, err
		}
		return map[string]string{"backup_id": bk.ID, "job_id": bk.JobID}, nil
	}})
	// Restoring a deleted server's backup (its final backup) onto another
	// server needs an owner: a delegate for this server may never have had
	// access to the deleted server's files.
	x.Register(BackupRestore, command.Handler{
		Signed: command.Always,
		OwnerOnly: func(ctx context.Context, e command.Envelope) (bool, error) {
			p, err := backupParams(e, true)
			if err != nil {
				return false, err
			}
			return b.FromDeletedServer(ctx, e.ServerID, p.BackupID)
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			p, err := backupParams(e, true)
			if err != nil {
				return nil, err
			}
			id, err := b.Restore(ctx, e.ServerID, p.BackupID, e.UserID)
			if err != nil {
				return nil, err
			}
			return jobResult(id), nil
		},
	})
	x.Register(BackupDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		p, err := backupParams(e, true)
		if err != nil {
			return nil, err
		}
		id, err := b.Delete(ctx, e.ServerID, p.BackupID)
		if err != nil {
			return nil, err
		}
		return jobResult(id), nil
	}})
	x.Register(BackupLock, command.Handler{
		Signed: func(_ context.Context, e command.Envelope) (bool, error) {
			p, err := backupParams(e, true)
			return !p.Locked, err
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			p, err := backupParams(e, true)
			if err != nil {
				return nil, err
			}
			return nil, b.Lock(ctx, e.ServerID, p.BackupID, p.Locked)
		},
	})
	policy := func(e command.Envelope) (backup.Policy, error) {
		var p backup.Policy
		if e.ServerID == "" {
			return p, errors.New("command needs a server_id")
		}
		return p, decode(e, &p)
	}
	x.Register(BackupPolicy, command.Handler{
		Signed: func(ctx context.Context, e command.Envelope) (bool, error) {
			p, err := policy(e)
			if err != nil {
				return false, err
			}
			cur, err := b.Policy(ctx, e.ServerID)
			if err != nil {
				return false, err
			}
			return p.KeepsLess(cur.Retention), nil
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			p, err := policy(e)
			if err != nil {
				return nil, err
			}
			return nil, b.SetPolicy(ctx, e.ServerID, p)
		},
	})
	x.Register(BackupDestinationSave, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DestinationParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, err := b.SaveDestination(ctx, p.Destination)
		if err != nil {
			return nil, err
		}
		return map[string]string{"destination_id": id}, nil
	}})
	x.Register(BackupDestinationDelete, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p DestinationParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, b.DeleteDestination(ctx, p.ID)
	}})
}

// SFTPParams are the params of node.sftp.
type SFTPParams struct {
	Enabled bool `json:"enabled"`
}

// RegisterSFTP adds node.sftp. It isn't signed: SFTP logins still need the
// Panel's grant (or a key it accepted), which is no more access than the web
// file manager gives (docs/SECURITY-MODEL.md#passkey-signed-commands). The
// result has the port and host key fingerprint to show users.
func RegisterSFTP(x *command.Executor, s *sftp.Service) {
	x.Register(NodeSFTP, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p SFTPParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return s.SetEnabled(ctx, p.Enabled)
	}})
}

// FilesParams are the params of the file actions; each uses the fields it
// needs. Paths are from the server's directory; Dir is what Names, Moves,
// and Changes are relative to.
type FilesParams struct {
	Path     string        `json:"path,omitempty"`
	Dir      string        `json:"dir,omitempty"`
	Names    []string      `json:"names,omitempty"`
	Moves    []files.Move  `json:"moves,omitempty"`
	Changes  []files.Chmod `json:"changes,omitempty"`
	Data     []byte        `json:"data,omitempty"` // files.write (base64 in JSON)
	Dest     string        `json:"dest,omitempty"` // files.decompress
	Size     int64         `json:"size,omitempty"` // files.upload
	UploadID string        `json:"upload_id,omitempty"`
}

// RegisterFiles adds the web file manager's actions
// (docs/WINGS.md#files-and-sftp). None are signed: file browsing and editing
// only need the Panel's grant (docs/SECURITY-MODEL.md#passkey-signed-commands),
// and the egg's denylist is enforced here whatever the Panel sends. Reads
// are read-only commands, so file contents aren't stored with the command.
func RegisterFiles(x *command.Executor, s *files.Service) {
	handler := func(readOnly bool, run func(context.Context, command.Envelope, FilesParams) (any, error)) command.Handler {
		return command.Handler{Signed: command.Never, ReadOnly: readOnly, Run: func(ctx context.Context, e command.Envelope) (any, error) {
			if e.ServerID == "" {
				return nil, errors.New("command needs a server_id")
			}
			var p FilesParams
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			return run(ctx, e, p)
		}}
	}
	jobResult := func(id string, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": id}, nil
	}

	x.Register(FilesList, handler(true, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return s.List(ctx, e.ServerID, p.Path)
	}))
	x.Register(FilesStat, handler(true, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return s.Stat(ctx, e.ServerID, p.Path)
	}))
	x.Register(FilesRead, handler(true, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return s.Read(ctx, e.ServerID, p.Path)
	}))
	x.Register(FilesDownload, handler(true, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return s.StartDownload(ctx, e.ServerID, p.Path)
	}))
	x.Register(FilesUploadStatus, handler(true, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		up, err := s.UploadStatus(ctx, p.UploadID)
		if err == nil && up.ServerID != e.ServerID {
			return nil, files.ErrTransferNotFound
		}
		return up, err
	}))

	x.Register(FilesWrite, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.Write(ctx, e.ServerID, p.Path, p.Data)
	}))
	x.Register(FilesMkdir, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.Mkdir(ctx, e.ServerID, p.Path)
	}))
	x.Register(FilesRename, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.Rename(ctx, e.ServerID, p.Dir, p.Moves)
	}))
	x.Register(FilesCopy, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		name, err := s.Copy(ctx, e.ServerID, p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]string{"path": name}, nil
	}))
	x.Register(FilesDelete, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.Delete(ctx, e.ServerID, p.Dir, p.Names)
	}))
	x.Register(FilesChmod, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.Chmod(ctx, e.ServerID, p.Dir, p.Changes)
	}))
	x.Register(FilesCompress, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return jobResult(s.Compress(ctx, e.ServerID, e.UserID, p.Dir, p.Names))
	}))
	x.Register(FilesDecompress, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return jobResult(s.Decompress(ctx, e.ServerID, e.UserID, p.Path, p.Dest))
	}))
	x.Register(FilesUpload, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return s.StartUpload(ctx, e.ServerID, e.UserID, p.Path, p.Size)
	}))
	x.Register(FilesUploadCancel, handler(false, func(ctx context.Context, e command.Envelope, p FilesParams) (any, error) {
		return nil, s.CancelUpload(ctx, e.ServerID, p.UploadID)
	}))
}

// decodeOptional decodes params that may be left out entirely.
func decodeOptional(e command.Envelope, v any) error {
	if len(e.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.Params, v); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}

func decode(e command.Envelope, v any) error {
	if len(e.Params) == 0 {
		return errors.New("command has no params")
	}
	if err := json.Unmarshal(e.Params, v); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}
