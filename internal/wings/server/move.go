package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Moving a server to another disk (docs/WINGS.md#data-disks): a job stops
// the server, copies its files and trash to the new disk, switches the
// server over, and only then removes the old copy. It starts the server
// again if it was running.

// Moving is the state of a server whose files are being moved. It's
// stopped, and power actions and file access are refused.
const Moving State = "moving"

// ErrMoving is returned for a server that's being moved.
var ErrMoving = errors.New("server is being moved to another disk")

// JobMove moves a server to another disk.
const JobMove = "server.move"

// EventMoved is a server's files having moved to another disk.
const EventMoved = "server.moved"

type movePayload struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Start bool   `json:"start,omitempty"`
	User  string `json:"user,omitempty"`
}

// Move queues moving a server's files to another disk ("" is the main
// volume). It returns the job's ID.
func (m *Manager) Move(ctx context.Context, id, to, user string) (string, error) {
	i, err := m.instance(id)
	if err != nil {
		return "", err
	}
	srv, err := m.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if srv.Disk == to {
		return "", fmt.Errorf("%w: the server is already on that disk", ErrInvalid)
	}
	vol, err := m.volumeFor(to)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoDisk, err)
	}
	if err := vol.Check(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoDisk, err)
	}
	switch i.getState() {
	case Installing:
		return "", ErrInstalling
	case Restoring:
		return "", ErrRestoring
	case Deleting:
		return "", ErrDeleting
	case Moving:
		return "", ErrMoving
	}
	if active, err := m.o.Jobs.HasActive(ctx, id); err != nil || active {
		return "", errors.Join(errors.New("the server has a job running or waiting; try again when it's done"), err)
	}
	// The new disk must hold the files, with room to spare.
	if u, err := m.DiskUsage(ctx, id); err == nil {
		if _, free, err := storage.Space(vol.Path); err == nil && u.Bytes+u.Bytes/10 > free {
			return "", fmt.Errorf("%w: the server uses %d MiB and the disk has %d MiB free", ErrInvalid, u.Bytes>>20, free>>20)
		}
	}
	start := i.isUp() || srv.DesiredState == "running"
	return m.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobMove, ServerID: id, Payload: movePayload{From: srv.Disk, To: to, Start: start, User: user}})
}

func (m *Manager) moveJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p movePayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	i, err := m.instance(j.ServerID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil //nolint:nilnil // deleted meanwhile; nothing to move
	}
	if err != nil {
		return nil, err
	}
	i.power.Lock()
	defer i.power.Unlock()
	if err := i.stopLocked(ctx, false, true); err != nil {
		m.log.Warn("stop before move failed; killing", "server", i.id, "err", err)
		if err := i.stopLocked(ctx, true, true); err != nil {
			return nil, err
		}
	}
	i.setState(Moving)
	err = m.move(ctx, i, p, log)
	if errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
		return nil, err // still Moving: the job resumes on the next start
	}
	i.setState(Offline)
	if err != nil {
		i.console.Notice("moving the server to another disk failed: %v", err)
	}
	if p.Start {
		if serr := i.startLocked(ctx, true); serr != nil {
			m.log.Warn("starting after the move failed", "server", i.id, "err", serr)
		}
	}
	return nil, err
}

// move copies, switches, and cleans up. Each step can be run again: the
// copy starts over, and once the server is on the new disk only the old
// copy is removed.
func (m *Manager) move(ctx context.Context, i *instance, p movePayload, log io.Writer) error {
	srv, err := m.Get(ctx, i.id)
	if err != nil {
		return err
	}
	fromRoot, err := m.diskRoot(p.From)
	if err != nil {
		return err
	}
	toRoot, err := m.diskRoot(p.To)
	if err != nil {
		return err
	}
	src, dst := filepath.Join(fromRoot, i.id), filepath.Join(toRoot, i.id)
	srcTrash, dstTrash := filepath.Join(fromRoot, TrashDirName, i.id), filepath.Join(toRoot, TrashDirName, i.id)
	switch srv.Disk {
	case p.To:
		// Switched before Wings stopped: only the old copy is left.
	case p.From:
		if err := m.copyServer(ctx, srv, src, dst, srcTrash, dstTrash, p, log); err != nil {
			if !errors.Is(context.Cause(ctx), jobs.ErrShutdown) {
				_ = os.RemoveAll(dst)
				_ = os.RemoveAll(dstTrash)
			}
			return err
		}
	default:
		return fmt.Errorf("the server is on disk %q, not %q", srv.Disk, p.From)
	}
	_, _ = fmt.Fprintln(log, "removing the old copy")
	if vol, err := m.volumeFor(p.From); err == nil && srv.QuotaProject != 0 {
		if err := vol.Release(srv.QuotaProject); err != nil {
			m.log.Warn("clearing the old disk's limit failed", "server", i.id, "err", err)
		}
	}
	for _, dir := range []string{src, srcTrash} {
		if err := os.RemoveAll(dir); err != nil {
			m.log.Warn("removing the old copy failed", "server", i.id, "dir", dir, "err", err)
		}
	}
	return nil
}

func (m *Manager) copyServer(ctx context.Context, srv *Server, src, dst, srcTrash, dstTrash string, p movePayload, log io.Writer) error {
	vol, err := m.volumeFor(p.To)
	if err != nil {
		return err
	}
	// A copy left by an earlier attempt starts over.
	for _, dir := range []string{dst, dstTrash} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := vol.Prepare(dst, srv.QuotaProject, srv.Limits.DiskMiB<<20, m.o.UID, m.o.GID); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "copying %s to %s\n", src, dst)
	if err := copyTree(ctx, src, dst); err != nil {
		return err
	}
	if _, err := os.Stat(srcTrash); err == nil {
		_, _ = fmt.Fprintln(log, "copying the trash")
		if err := os.MkdirAll(filepath.Dir(dstTrash), 0o700); err != nil {
			return err
		}
		if err := os.MkdirAll(dstTrash, 0o700); err != nil {
			return err
		}
		if err := copyTree(ctx, srcTrash, dstTrash); err != nil {
			return err
		}
	}
	var moved, updated Event
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.SetServerDisk(ctx, store.SetServerDiskParams{DiskID: p.To, ID: srv.ID}); err != nil {
			return err
		}
		row, err := q.GetServer(ctx, srv.ID)
		if err != nil {
			return err
		}
		updated = newEvent(EventUpdated, srv.ID, row.Version, nil)
		moved = newEvent(EventMoved, srv.ID, row.Version, map[string]any{"from": diskName(p.From), "to": diskName(p.To), "user": p.User})
		if err := publishTx(ctx, q, updated); err != nil {
			return err
		}
		return publishTx(ctx, q, moved)
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	if i, ok := m.servers[srv.ID]; ok {
		i.disk = p.To
	}
	m.mu.Unlock()
	m.emit(updated)
	m.emit(moved)
	_, _ = fmt.Fprintln(log, "the server is on the new disk")
	return nil
}

// diskName is a disk as the API names it.
func diskName(disk string) string {
	if disk == "" {
		return "main"
	}
	return disk
}

// copyTree copies a directory's contents into another, keeping owners,
// modes, times, links (never followed), and extended attributes.
func copyTree(ctx context.Context, src, dst string) error {
	//nolint:gosec // both are server directories under Raptor's disks
	out, err := exec.CommandContext(ctx, "cp", "-a", src+"/.", dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copying the files: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
