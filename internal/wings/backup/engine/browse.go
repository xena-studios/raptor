package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"slices"
	"strings"

	kfs "github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/restore"
	"github.com/kopia/kopia/snapshot/snapshotfs"

	"github.com/xena-studios/raptor/internal/wings/files"
)

// Browsing a backup, and pulling some of its files out
// (docs/WINGS.md#backups): the egg's file_denylist applies as it does to the
// live files, so a backup can't hand out what the file manager wouldn't.

// BrowseRequest lists one folder of a snapshot.
type BrowseRequest struct {
	SnapshotID string   `json:"snapshot_id"`
	Path       string   `json:"path"` // "" for the server's directory
	Deny       []string `json:"deny,omitempty"`
}

// BrowseEntry is a file or folder in a snapshot.
type BrowseEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file", "dir", or "symlink"
	// Size is a file's size, or everything in a folder.
	Size       int64 `json:"size"`
	ModifiedAt int64 `json:"modified_at"` // unix ms
	Denied     bool  `json:"denied,omitempty"`
}

// ExtractRequest restores some of a snapshot's files and folders into a
// folder of their own, leaving everything else alone.
type ExtractRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Dir        string `json:"dir"`  // the server's directory
	Into       string `json:"into"` // relative to Dir; created, and must not exist
	// Paths are relative to the server's directory, as Browse names them.
	Paths []string `json:"paths"`
	Deny  []string `json:"deny,omitempty"`
	UID   int      `json:"uid"`
	GID   int      `json:"gid"`
	// MaxSize > 0 refuses more than this many bytes, before anything is
	// written.
	MaxSize int64 `json:"max_size,omitempty"`
}

// ErrBadPath is a path that isn't in the snapshot, or isn't allowed.
var ErrBadPath = errors.New("no such file or folder in the backup")

// cleanPath checks a path names something inside the server's directory.
func cleanPath(p string) ([]string, error) {
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return nil, nil
	}
	if path.Clean(p) != p || strings.HasPrefix(p, "../") || p == ".." {
		return nil, fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	return strings.Split(p, "/"), nil
}

func (e *Engine) snapshotRoot(ctx context.Context, id string) (kfs.Entry, repo.Repository, error) {
	rep, err := e.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	man, err := snapshot.LoadSnapshot(ctx, rep, manifest.ID(id))
	if err != nil {
		_ = rep.Close(ctx)
		return nil, nil, err
	}
	root, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		_ = rep.Close(ctx)
		return nil, nil, err
	}
	return root, rep, nil
}

func entryType(e kfs.Entry) string {
	switch e.(type) {
	case kfs.Directory:
		return "dir"
	case kfs.Symlink:
		return "symlink"
	}
	return "file"
}

// Browse lists one folder of a snapshot.
func (e *Engine) Browse(ctx context.Context, req BrowseRequest) ([]BrowseEntry, error) {
	elems, err := cleanPath(req.Path)
	if err != nil {
		return nil, err
	}
	root, rep, err := e.snapshotRoot(ctx, req.SnapshotID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rep.Close(ctx) }()
	deny := files.Compile(req.Deny)
	if deny.Denied(strings.Join(elems, "/"), true) {
		return nil, fmt.Errorf("%w: %q", ErrBadPath, req.Path)
	}
	ent, err := snapshotfs.GetNestedEntry(ctx, root, elems)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrBadPath, req.Path)
	}
	dir, ok := ent.(kfs.Directory)
	if !ok {
		return nil, fmt.Errorf("%w: %q isn't a folder", ErrBadPath, req.Path)
	}
	children, err := kfs.GetAllEntries(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]BrowseEntry, 0, len(children))
	for _, c := range children {
		t := entryType(c)
		out = append(out, BrowseEntry{
			Name: c.Name(), Type: t, Size: c.Size(), ModifiedAt: c.ModTime().UnixMilli(),
			Denied: deny.Denied(path.Join(path.Join(elems...), c.Name()), t == "dir"),
		})
	}
	return out, nil
}

// Extract restores req.Paths (with everything in the folders among them)
// into req.Into, which it creates. Denied files are left out.
func (e *Engine) Extract(ctx context.Context, req ExtractRequest, progress func(Progress)) (RestoreResult, error) {
	if len(req.Paths) == 0 {
		return RestoreResult{}, errors.New("pick something to restore")
	}
	picks := map[string]bool{}
	for _, p := range req.Paths {
		elems, err := cleanPath(p)
		if err != nil {
			return RestoreResult{}, err
		}
		if elems == nil {
			return RestoreResult{}, errors.New("to restore everything, restore the whole backup")
		}
		picks[strings.Join(elems, "/")] = true
	}
	intoElems, err := cleanPath(req.Into)
	if err != nil || intoElems == nil {
		return RestoreResult{}, fmt.Errorf("bad destination %q", req.Into)
	}
	root, rep, err := e.snapshotRoot(ctx, req.SnapshotID)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = rep.Close(ctx) }()
	deny := files.Compile(req.Deny)

	// Everything picked must exist and be allowed; their sizes add up to
	// what's restored (a folder's size covers what's in it).
	var total int64
	for p := range picks {
		ent, err := snapshotfs.GetNestedEntry(ctx, root, strings.Split(p, "/"))
		if err != nil || deny.Denied(p, entryType(ent) == "dir") {
			return RestoreResult{}, fmt.Errorf("%w: %q", ErrBadPath, p)
		}
		if !picked(picks, path.Dir(p)) {
			total += ent.Size()
		}
	}
	if req.MaxSize > 0 && total > req.MaxSize {
		return RestoreResult{}, fmt.Errorf("%w (%d bytes, limit %d)", ErrTooLarge, total, req.MaxSize)
	}
	dir, ok := root.(kfs.Directory)
	if !ok {
		return RestoreResult{}, errors.New("the backup's root isn't a folder")
	}

	server, err := os.OpenRoot(req.Dir)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = server.Close() }()
	into := path.Join(intoElems...)
	// Each new folder on the way, owned by the server's user.
	for i := range intoElems {
		p := path.Join(intoElems[:i+1]...)
		err := server.Mkdir(p, 0o755)
		if errors.Is(err, os.ErrExist) && i < len(intoElems)-1 {
			continue
		}
		if err != nil {
			return RestoreResult{}, err
		}
		if req.UID >= 0 {
			if err := server.Lchown(p, req.UID, req.GID); err != nil {
				return RestoreResult{}, err
			}
		}
	}
	out, err := server.OpenRoot(into)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = out.Close() }()
	st, err := restore.Entry(ctx, rep, &rootOutput{root: out, uid: req.UID, gid: req.GID},
		&pickDir{Directory: dir, picks: picks, deny: deny}, restore.Options{
			Parallel:               2,
			RestoreDirEntryAtDepth: math.MaxInt32,
			ProgressCallback: func(_ context.Context, s restore.Stats) {
				if progress != nil {
					progress(Progress{Bytes: s.RestoredTotalFileSize, Total: total})
				}
			},
		})
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Size: st.RestoredTotalFileSize, Files: int64(st.RestoredFileCount)}, nil
}

// picked reports whether p or a folder it's in was picked.
func picked(picks map[string]bool, p string) bool {
	for p != "." && p != "" {
		if picks[p] {
			return true
		}
		p = path.Dir(p)
	}
	return false
}

// pickDir is a snapshot folder showing only what was picked: the picked
// files and folders (all of a picked folder), and the folders on the way
// to them. Denied entries are never shown.
type pickDir struct {
	kfs.Directory
	rel   string // "" for the root
	all   bool   // inside a picked folder
	picks map[string]bool
	deny  *files.Denylist
}

func (d *pickDir) keep(e kfs.Entry) (kfs.Entry, bool) {
	p := path.Join(d.rel, e.Name())
	isDir := entryType(e) == "dir"
	if d.deny.Denied(p, isDir) {
		return nil, false
	}
	all := d.all || d.picks[p]
	if !all {
		// On the way to a pick?
		prefix := p + "/"
		if !isDir || !slices.ContainsFunc(keys(d.picks), func(k string) bool { return strings.HasPrefix(k, prefix) }) {
			return nil, false
		}
	}
	if sub, ok := e.(kfs.Directory); ok {
		return &pickDir{Directory: sub, rel: p, all: all, picks: d.picks, deny: d.deny}, true
	}
	return e, true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (d *pickDir) Child(ctx context.Context, name string) (kfs.Entry, error) {
	e, err := d.Directory.Child(ctx, name)
	if err != nil {
		return nil, err
	}
	k, ok := d.keep(e)
	if !ok {
		return nil, kfs.ErrEntryNotFound
	}
	return k, nil
}

func (d *pickDir) Iterate(ctx context.Context) (kfs.DirectoryIterator, error) {
	it, err := d.Directory.Iterate(ctx)
	if err != nil {
		return nil, err
	}
	return &pickIter{it: it, d: d}, nil
}

type pickIter struct {
	it kfs.DirectoryIterator
	d  *pickDir
}

func (p *pickIter) Next(ctx context.Context) (kfs.Entry, error) {
	for {
		e, err := p.it.Next(ctx)
		if e == nil || err != nil {
			return e, err
		}
		if k, ok := p.d.keep(e); ok {
			return k, nil
		}
	}
}

func (p *pickIter) Close() { p.it.Close() }
