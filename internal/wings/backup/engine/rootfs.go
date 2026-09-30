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
