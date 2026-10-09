package server

import (
	"context"
	"errors"
	"fmt"
)

// Restoring is the state of a server whose files are being replaced by a
// backup. It's stopped until the restore ends.
const Restoring State = "restoring"

// ErrRestoring is returned for a server that's being restored.
var ErrRestoring = errors.New("server is being restored from a backup")

// BackupSource is what backing up a server needs.
type BackupSource struct {
	Dir string
	// Hooks from the egg's x-raptor block, run while the server is up:
	// Pre before the backup (e.g. save-off, save-all), Post after (save-on).
	Pre, Post []string
	// WaitFor is console output that says the Pre commands are done ("" =
	// don't wait).
	WaitFor   string
	DiskLimit int64 // bytes; 0 = none
	UID, GID  int   // the owner of restored files
	// DesiredRunning: the server is meant to be running.
	DesiredRunning bool
	// Denylist is the egg's file_denylist: what browsing and extracting a
	// backup leave out, as the file manager does.
	Denylist []string
}

// BackupSource returns what backing up the server needs.
func (m *Manager) BackupSource(ctx context.Context, id string) (BackupSource, error) {
	if _, err := m.instance(id); err != nil {
		return BackupSource{}, err
	}
	srv, err := m.Get(ctx, id)
	if err != nil {
		return BackupSource{}, err
	}
	dir, err := m.serverDir(id)
	if err != nil {
		return BackupSource{}, err
	}
	h := srv.Egg().Raptor.Backup
	return BackupSource{
		Dir: dir, Pre: h.Pre, Post: h.Post, WaitFor: h.WaitFor,
		DiskLimit: srv.Limits.DiskMiB << 20, UID: m.o.UID, GID: m.o.GID,
		DesiredRunning: srv.DesiredState == "running", Denylist: srv.Egg().FileDenylist,
	}, nil
}

// Restore stops the server (it stays stopped if anything fails) and runs fn
// on its directory with the server in the Restoring state. Afterwards the
// server is started if start is set. Power actions wait until it's done.
func (m *Manager) Restore(ctx context.Context, id string, start bool, fn func(ctx context.Context, dir string) error) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	switch i.getState() {
	case Installing:
		return ErrInstalling
	case Restoring:
		return ErrRestoring
	}
	// Stopped for good until the restore is done: if Wings stops meanwhile,
	// a half-restored server must not be started again.
	if err := i.stopLocked(ctx, false, true); err != nil {
		m.log.Warn("stop before restore failed; killing", "server", id, "err", err)
		if err := i.stopLocked(ctx, true, true); err != nil {
			return err
		}
	}
	dir, err := m.serverDir(id)
	if err != nil {
		return err
	}
	srv, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := m.prepareStorage(ctx, srv); err != nil {
		return fmt.Errorf("prepare storage: %w", err)
	}
	i.setState(Restoring)
	i.console.Notice("restoring a backup")
	err = fn(ctx, dir)
	i.setState(Offline)
	if err != nil {
		i.console.Notice("restore failed: %v", err)
		return err
	}
	i.console.Notice("backup restored")
	if start {
		return i.startLocked(ctx, true)
	}
	return nil
}
