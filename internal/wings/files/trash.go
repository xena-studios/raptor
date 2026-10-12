package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// The trash (docs/WINGS.md#trash): what the web file manager deletes is
// moved to <disk>/.trash/<server>/<entry>/<name>, outside the server's
// directory (so the game, SFTP, and backups don't see it) and on the same
// filesystem (so moving is instant, and it still counts against the
// server's disk limit). It can be restored where it was, deleted for good,
// and goes by itself after TrashKeep.

// TrashKeep is how long deleted files are kept.
const TrashKeep = 7 * 24 * time.Hour

// ErrTrashNotFound means a trash entry doesn't exist (for this server).
var ErrTrashNotFound = errors.New("that's not in the trash any more")

// TrashEntry is something in a server's trash.
type TrashEntry struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // where it was
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	DeletedAt int64  `json:"deleted_at"` // unix ms
	DeletedBy string `json:"deleted_by"`
}

func trashEntry(r store.Trash) TrashEntry {
	return TrashEntry{ID: r.ID, Path: r.Path, IsDir: r.IsDir == 1, Size: r.Size, DeletedAt: r.DeletedAt, DeletedBy: r.DeletedBy}
}

// trashDir is a server's trash directory, made if needed.
func (s *Service) trashDir(id string) (string, error) {
	dir, err := s.o.Servers.TrashDir(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Trash moves files and directories (relative to dir) to the server's
// trash. A name that's already gone is skipped; it stops at the first
// failure.
func (s *Service) Trash(ctx context.Context, id, user, dir string, names []string) error {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tdir, err := s.trashDir(id)
	if err != nil {
		return err
	}
	for _, n := range names {
		name := Rel(Join(dir, n))
		if name == "." {
			return &fs.PathError{Op: "trash", Path: name, Err: ErrRoot}
		}
		fi, err := f.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		entry, err := uuid.NewV7()
		if err != nil {
			return err
		}
		edir := filepath.Join(tdir, entry.String())
		if err := os.Mkdir(edir, 0o700); err != nil {
			return err
		}
		ed, err := os.Open(edir) //nolint:gosec // an entry directory Wings made, named by a UUID
		if err != nil {
			return err
		}
		base := path.Base(name)
		err = f.MoveOut(name, ed, base)
		_ = ed.Close()
		if err != nil {
			_ = os.Remove(edir)
			return err
		}
		size := treeSize(filepath.Join(edir, base))
		if err := s.o.Store.Write.InsertTrash(context.WithoutCancel(ctx), store.InsertTrashParams{
			ID: entry.String(), ServerID: id, Path: "/" + name, IsDir: boolInt(fi.IsDir()), Size: size,
			DeletedAt: s.o.Now().UnixMilli(), DeletedBy: user,
		}); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// treeSize is the bytes of regular files under p, not following links.
func treeSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a size is a hint
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ListTrash lists a server's trash, newest first.
func (s *Service) ListTrash(ctx context.Context, id string) ([]TrashEntry, error) {
	rows, err := s.o.Store.Read.ListTrash(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]TrashEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, trashEntry(r))
	}
	return out, nil
}

// Restored is where an entry went back to.
type Restored struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Restore puts entries back where they were. If something new is there
// now, it's restored beside it as "name (restored)".
func (s *Service) Restore(ctx context.Context, id string, entries []string) ([]Restored, error) {
	f, err := s.open(ctx, id, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	tdir, err := s.trashDir(id)
	if err != nil {
		return nil, err
	}
	var out []Restored
	for _, e := range entries {
		r, err := s.o.Store.Read.GetTrash(ctx, store.GetTrashParams{ID: e, ServerID: id})
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrTrashNotFound
		}
		if err != nil {
			return out, err
		}
		edir := filepath.Join(tdir, r.ID)
		ed, err := os.Open(edir) //nolint:gosec // an entry directory Wings made, named by a UUID
		if err != nil {
			return out, ErrTrashNotFound
		}
		base := path.Base(r.Path)
		to, err := s.restoreAs(f, ed, base, Rel(r.Path))
		_ = ed.Close()
		if err != nil {
			return out, err
		}
		_ = os.Remove(edir)
		if err := s.o.Store.Write.DeleteTrash(context.WithoutCancel(ctx), r.ID); err != nil {
			return out, err
		}
		out = append(out, Restored{ID: r.ID, Path: "/" + to})
	}
	return out, nil
}

// restoreAs moves an entry to name, or "name (restored)", "name (restored
// 2)", … if something's there now.
func (s *Service) restoreAs(f *FS, ed *os.File, base, name string) (string, error) {
	dir, file := path.Split(name)
	ext := path.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if ext == file { // ".env": no stem
		stem, ext = file, ""
	}
	for i := range 20 {
		to := name
		if i == 1 {
			to = path.Join(dir, fmt.Sprintf("%s (restored)%s", stem, ext))
		} else if i > 1 {
			to = path.Join(dir, fmt.Sprintf("%s (restored %d)%s", stem, i, ext))
		}
		err := f.MoveIn(ed, base, to)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return to, err
	}
	return "", fmt.Errorf("%s: too many restored copies there already", name)
}

// DeleteTrash deletes entries for good.
func (s *Service) DeleteTrash(ctx context.Context, id string, entries []string) error {
	tdir, err := s.o.Servers.TrashDir(id)
	if err != nil {
		return err
	}
	for _, e := range entries {
		r, err := s.o.Store.Read.GetTrash(ctx, store.GetTrashParams{ID: e, ServerID: id})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.purge(ctx, tdir, r.ID); err != nil {
			return err
		}
	}
	return nil
}

// EmptyTrash deletes everything in a server's trash for good.
func (s *Service) EmptyTrash(ctx context.Context, id string) error {
	rows, err := s.o.Store.Read.ListTrash(ctx, id)
	if err != nil {
		return err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return s.DeleteTrash(ctx, id, ids)
}

func (s *Service) purge(ctx context.Context, tdir, entry string) error {
	if _, err := uuid.Parse(entry); err != nil {
		return err // never a path from the database that isn't an entry
	}
	if err := os.RemoveAll(filepath.Join(tdir, entry)); err != nil {
		return err
	}
	return s.o.Store.Write.DeleteTrash(context.WithoutCancel(ctx), entry)
}

// PruneTrash deletes what's been in any trash longer than TrashKeep.
func (s *Service) PruneTrash(ctx context.Context) (int, error) {
	rows, err := s.o.Store.Read.TrashBefore(ctx, s.o.Now().Add(-TrashKeep).UnixMilli())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		tdir, err := s.o.Servers.TrashDir(r.ServerID)
		if err != nil {
			continue
		}
		if err := s.purge(ctx, tdir, r.ID); err != nil {
			s.o.Log.Warn("emptying old trash failed", "server", r.ServerID, "err", err)
			continue
		}
		n++
	}
	return n, nil
}
