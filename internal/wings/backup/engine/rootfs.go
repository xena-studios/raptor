package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"

	kfs "github.com/kopia/kopia/fs"
)

// The snapshot source: a Kopia filesystem over an os.Root, so nothing a game
// server puts in its directory (symlinks, a directory swapped for a symlink
// mid-backup) can make the backup read outside of it
// (docs/SECURITY-MODEL.md#host-side-file-safety). Only regular files,
// directories, and symlinks are backed up; symlinks are stored, never
// followed.

func newEntry(root *os.Root, rel string, fi os.FileInfo) kfs.Entry {
	e := rootEntry{FileInfo: fi, root: root, rel: rel}
	switch {
	case fi.IsDir():
		return &rootDir{e}
	case fi.Mode().IsRegular():
		return &rootFile{e}
	case fi.Mode()&fs.ModeSymlink != 0:
		return &rootSymlink{e}
	}
	return nil // devices, sockets, pipes
}

type rootEntry struct {
	os.FileInfo
	root *os.Root
	rel  string // "." for the root
}

func (e *rootEntry) Owner() kfs.OwnerInfo {
	if st, ok := e.Sys().(*syscall.Stat_t); ok {
		return kfs.OwnerInfo{UserID: st.Uid, GroupID: st.Gid}
	}
	return kfs.OwnerInfo{}
}

func (e *rootEntry) Device() kfs.DeviceInfo      { return kfs.DeviceInfo{} }
func (e *rootEntry) LocalFilesystemPath() string { return "" }
func (e *rootEntry) Close()                      {}

type rootDir struct{ rootEntry }

func (d *rootDir) Child(_ context.Context, name string) (kfs.Entry, error) {
	rel := path.Join(d.rel, name)
	fi, err := d.root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, kfs.ErrEntryNotFound
	}
	if err != nil {
		return nil, err
	}
	if e := newEntry(d.root, rel, fi); e != nil {
		return e, nil
	}
	return nil, kfs.ErrEntryNotFound
}

func (d *rootDir) Iterate(context.Context) (kfs.DirectoryIterator, error) {
	f, err := d.root.Open(d.rel)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return &dirIter{d: d, names: names}, nil
}

func (d *rootDir) SupportsMultipleIterations() bool { return true }

// dirIter stats entries as it goes, each through the root. Entries that
// vanished since the listing are skipped.
type dirIter struct {
	d     *rootDir
	names []string
}

func (it *dirIter) Next(context.Context) (kfs.Entry, error) {
	for len(it.names) > 0 {
		rel := path.Join(it.d.rel, it.names[0])
		it.names = it.names[1:]
		fi, err := it.d.root.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if e := newEntry(it.d.root, rel, fi); e != nil {
			return e, nil
		}
	}
	return nil, nil //nolint:nilnil // the end of the directory
}

func (it *dirIter) Close() {}

type rootFile struct{ rootEntry }

// Open opens the file non-blocking, so a file swapped for a named pipe after
// it was listed can't hang the backup, and checks it's still a regular file.
func (f *rootFile) Open(context.Context) (kfs.Reader, error) {
	file, err := f.root.OpenFile(f.rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := file.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is no longer a regular file", f.rel)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &fileReader{File: file, e: &rootFile{rootEntry{FileInfo: fi, root: f.root, rel: f.rel}}}, nil
}

type fileReader struct {
	*os.File
	e kfs.Entry
}

func (r *fileReader) Entry() (kfs.Entry, error) { return r.e, nil }

var _ io.ReadSeekCloser = (*fileReader)(nil)

type rootSymlink struct{ rootEntry }

func (s *rootSymlink) Readlink(context.Context) (string, error) { return s.root.Readlink(s.rel) }

// Resolve isn't supported: symlinks are backed up as links.
func (s *rootSymlink) Resolve(context.Context) (kfs.Entry, error) {
	return nil, errors.New("symlinks aren't followed")
}

// withExtra adds folders from outside the server's directory at the top
// of a snapshot (SnapshotRequest.Extra). The returned func closes them.
func withExtra(dir kfs.Directory, extra map[string]string) (kfs.Entry, func(), error) {
	top := &extraDir{Directory: dir, extra: map[string]kfs.Entry{}}
	var roots []*os.Root
	closeAll := func() {
		for _, r := range roots {
			_ = r.Close()
		}
	}
	for name, p := range extra {
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			closeAll()
			return nil, nil, fmt.Errorf("bad extra folder name %q", name)
		}
		r, err := os.OpenRoot(p)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		roots = append(roots, r)
		fi, err := r.Stat(".")
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		top.extra[name] = &namedDir{rootDir: &rootDir{rootEntry{FileInfo: fi, root: r, rel: "."}}, name: name}
	}
	return top, closeAll, nil
}

// extraDir is the server's directory with the extra folders in it.
type extraDir struct {
	kfs.Directory
	extra map[string]kfs.Entry
}

func (d *extraDir) Child(ctx context.Context, name string) (kfs.Entry, error) {
	if e, ok := d.extra[name]; ok {
		return e, nil
	}
	return d.Directory.Child(ctx, name)
}

func (d *extraDir) Iterate(ctx context.Context) (kfs.DirectoryIterator, error) {
	it, err := d.Directory.Iterate(ctx)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var all []kfs.Entry
	for {
		e, err := it.Next(ctx)
		if err != nil {
			return nil, err
		}
		if e == nil {
			break
		}
		if _, shadowed := d.extra[e.Name()]; !shadowed {
			all = append(all, e)
		}
	}
	for _, e := range d.extra {
		all = append(all, e)
	}
	slices.SortFunc(all, func(a, b kfs.Entry) int { return strings.Compare(a.Name(), b.Name()) })
	return &listIter{entries: all}, nil
}

// namedDir is an extra folder, under its name in the snapshot.
type namedDir struct {
	*rootDir
	name string
}

func (d *namedDir) Name() string { return d.name }

type listIter struct{ entries []kfs.Entry }

func (it *listIter) Next(context.Context) (kfs.Entry, error) {
	if len(it.entries) == 0 {
		return nil, nil //nolint:nilnil // the end of the directory
	}
	e := it.entries[0]
	it.entries = it.entries[1:]
	return e, nil
}

func (it *listIter) Close() {}
