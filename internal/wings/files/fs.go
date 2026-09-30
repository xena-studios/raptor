// Package files is how Wings touches a server's files for users: the web
// file manager's operations, transfers, archives, and SFTP. Everything goes
// through an os.Root on the server's directory, so nothing outside it can be
// reached: absolute paths, "..", and symlinks pointing out are all refused
// (docs/SECURITY-MODEL.md#server-files). The directory is written by code
// Raptor doesn't trust, so FIFOs, device nodes, and sockets are never opened,
// and the egg's file_denylist is enforced here for every caller.
package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Modes of what Wings creates for users. Wings runs with UMask=0077, so
// they're set explicitly after creating.
const (
	FileMode = 0o644
	DirMode  = 0o755
)

// Errors a caller can distinguish.
var (
	ErrNotRegular = errors.New("not a regular file")
	ErrDenied     = errors.New("the egg doesn't allow access to this file")
	ErrRoot       = errors.New("the server's directory itself can't be changed")
	ErrTooLarge   = errors.New("file too large")
)

// FS is one server's directory. It isn't tied to a request, so callers
// check that the server's files may be touched (not installing or
// restoring) themselves.
type FS struct {
	root     *os.Root
	uid, gid int
	deny     *Denylist
}

// New returns an FS on root. New files belong to uid:gid (the servers'
// user). deny may be nil.
func New(root *os.Root, uid, gid int, deny *Denylist) *FS {
	return &FS{root: root, uid: uid, gid: gid, deny: deny}
}

// Open opens dir as an FS.
func Open(dir string, uid, gid int, deny *Denylist) (*FS, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return New(root, uid, gid, deny), nil
}

// Close closes the root.
func (f *FS) Close() error { return f.root.Close() }

// Rel turns a user's path (absolute from the server's directory, or
// relative to it) into a path relative to the root; "." is the directory
// itself. ".." can't climb above it.
func Rel(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return p
}

// Denied reports whether name is on the egg's denylist, by its own name or,
// through symlinks, the file it reaches. isDir is used when name doesn't
// exist yet.
func (f *FS) Denied(name string, isDir bool) bool {
	if f.deny.Empty() {
		return false
	}
	name = Rel(name)
	if f.deny.Denied(name, f.isDir(name, isDir)) {
		return true
	}
	real := f.resolve(name)
	return real != name && f.deny.Denied(real, f.isDir(real, isDir))
}

func (f *FS) isDir(name string, dflt bool) bool {
	if fi, err := f.root.Lstat(name); err == nil {
		return fi.IsDir()
	}
	return dflt
}

// maxLinks is how many symlinks resolve follows, as Linux does.
const maxLinks = 40

// resolve follows symlinks in name the way os.Root does, so a link (made
// by the user or planted by the server) can't reach a denied file under
// another name. What doesn't exist yet is kept as given. A link os.Root
// would refuse (absolute, or leaving the directory) is returned as is: it
// can't be opened anyway.
func (f *FS) resolve(name string) string {
	parts := strings.Split(name, "/")
	cur := ""
	for hops := 0; len(parts) > 0; {
		seg := parts[0]
		parts = parts[1:]
		if seg == "" || seg == "." {
			continue
		}
		next := path.Join(cur, seg)
		fi, err := f.root.Lstat(next)
		if err != nil {
			return path.Join(append([]string{next}, parts...)...)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		hops++
		target, err := f.root.Readlink(next)
		if hops > maxLinks || err != nil || path.IsAbs(target) {
			return path.Join(append([]string{next}, parts...)...)
		}
		t := path.Join(cur, target)
		if t == ".." || strings.HasPrefix(t, "../") {
			return path.Join(append([]string{next}, parts...)...)
		}
		parts = append(strings.Split(t, "/"), parts...)
		cur = ""
	}
	if cur == "" {
		return "."
	}
	return cur
}

// check refuses denied paths.
func (f *FS) check(name string, isDir bool) error {
	if f.Denied(name, isDir) {
		return &fs.PathError{Op: "access", Path: name, Err: ErrDenied}
	}
	return nil
}

// checkTree refuses name if it or anything under it is denied, so moving or
// deleting a directory can't reach a denied file inside it. With to set,
// the same names under to (where a move puts them) must be allowed too.
func (f *FS) checkTree(name, to string) error {
	if f.deny.Empty() {
		return nil
	}
	if err := f.check(name, false); err != nil {
		return err
	}
	if to != "" {
		if err := f.check(to, false); err != nil {
			return err
		}
	}
	fi, err := f.root.Lstat(name)
	if err != nil || !fi.IsDir() {
		return nil //nolint:nilerr // the operation reports a missing file itself
	}
	return fs.WalkDir(f.root.FS(), name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if f.deny.Denied(p, d.IsDir()) {
			return &fs.PathError{Op: "access", Path: p, Err: ErrDenied}
		}
		if to != "" && f.deny.Denied(to+strings.TrimPrefix(p, name), d.IsDir()) {
			return &fs.PathError{Op: "access", Path: to + strings.TrimPrefix(p, name), Err: ErrDenied}
		}
		return nil
	})
}

// Open opens a regular file. Anything else (a FIFO, a device node a hostile
// install script left behind, a directory) is refused before it's opened for
// real, and checked again on the open file in case it was swapped in between.
// O_NONBLOCK keeps a FIFO swapped in from blocking the open. New files, and
// files root owns (left by an install script), are given to the servers'
// user.
func (f *FS) Open(name string, flag int) (*os.File, error) {
	name = Rel(name)
	if err := f.check(name, false); err != nil {
		return nil, err
	}
	fi, err := f.root.Stat(name)
	created := errors.Is(err, fs.ErrNotExist) && flag&os.O_CREATE != 0
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return nil, &fs.PathError{Op: "open", Path: name, Err: ErrNotRegular}
	case err != nil && !created:
		return nil, err
	}
	file, err := f.root.OpenFile(name, flag|syscall.O_NONBLOCK|syscall.O_NOCTTY, FileMode)
	if err != nil {
		return nil, err
	}
	st, err := file.Stat()
	if err == nil && !st.Mode().IsRegular() {
		err = &fs.PathError{Op: "open", Path: name, Err: ErrNotRegular}
	}
	if err == nil && created {
		err = file.Chmod(FileMode)
	}
	if err == nil && (created || owner(st) == 0) {
		err = file.Chown(f.uid, f.gid)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func owner(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// Stat follows a symlink (inside the directory).
func (f *FS) Stat(name string) (fs.FileInfo, error) { return f.root.Stat(Rel(name)) }

// Lstat doesn't follow symlinks.
func (f *FS) Lstat(name string) (fs.FileInfo, error) { return f.root.Lstat(Rel(name)) }

// Readlink returns a symlink's target as stored.
func (f *FS) Readlink(name string) (string, error) {
	name = Rel(name)
	if err := f.check(name, false); err != nil {
		return "", err
	}
	return f.root.Readlink(name)
}

// List reads a directory. O_DIRECTORY makes opening anything else fail
// right away (a FIFO would otherwise block). Entries are in name order;
// denied files are listed.
func (f *FS) List(name string) ([]fs.FileInfo, error) {
	file, err := f.root.OpenFile(Rel(name), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed while listing
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, fi)
	}
	return out, nil
}

// Mkdir creates a directory owned by the servers' user.
func (f *FS) Mkdir(name string) error {
	name = Rel(name)
	if name == "." {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrExist}
	}
	if err := f.check(name, true); err != nil {
		return err
	}
	if err := f.root.Mkdir(name, DirMode); err != nil {
		return err
	}
	if err := f.root.Chmod(name, DirMode); err != nil {
		return err
	}
	return f.root.Lchown(name, f.uid, f.gid)
}

// MkdirAll creates a directory and any missing parents, each owned by the
// servers' user.
func (f *FS) MkdirAll(name string) error {
	name = Rel(name)
	if name == "." {
		return nil
	}
	cur := ""
	for seg := range strings.SplitSeq(name, "/") {
		cur = path.Join(cur, seg)
		fi, err := f.root.Stat(cur)
		if err == nil {
			if !fi.IsDir() {
				return &fs.PathError{Op: "mkdir", Path: cur, Err: syscall.ENOTDIR}
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := f.Mkdir(cur); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}

// Rename moves from to to. Without replace an existing target is refused.
// A directory can't be moved if it holds denied files, or if its files would
// land on denied names.
func (f *FS) Rename(from, to string, replace bool) error {
	from, to = Rel(from), Rel(to)
	if from == "." || to == "." {
		return &fs.PathError{Op: "rename", Path: from, Err: ErrRoot}
	}
	if err := f.checkTree(from, to); err != nil {
		return err
	}
	if !replace {
		if _, err := f.root.Lstat(to); err == nil {
			return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
		}
	}
	return f.root.Rename(from, to)
}

// Remove removes a file or an empty directory.
func (f *FS) Remove(name string) error {
	name = Rel(name)
	if name == "." {
		return &fs.PathError{Op: "remove", Path: name, Err: ErrRoot}
	}
	if err := f.check(name, false); err != nil {
		return err
	}
	return f.root.Remove(name)
}

// RemoveAll removes a file or a directory and everything in it. Symlinks
// are removed, never their targets. A directory holding denied files is
// refused.
func (f *FS) RemoveAll(name string) error {
	name = Rel(name)
	if name == "." {
		return &fs.PathError{Op: "remove", Path: name, Err: ErrRoot}
	}
	if err := f.checkTree(name, ""); err != nil {
		return err
	}
	if _, err := f.root.Lstat(name); err != nil {
		return err
	}
	return f.root.RemoveAll(name)
}

// Chmod sets permission bits only: never setuid, setgid, or sticky.
func (f *FS) Chmod(name string, mode fs.FileMode) error {
	name = Rel(name)
	if err := f.check(name, false); err != nil {
		return err
	}
	return f.root.Chmod(name, mode.Perm())
}

// Symlink creates link pointing at target, stored as given. It may point
// anywhere: Wings never follows a link out of the server's directory, and
// in the container it resolves inside the container, as a link the server
// made itself would.
func (f *FS) Symlink(target, link string) error {
	link = Rel(link)
	if err := f.check(link, false); err != nil {
		return err
	}
	if err := f.root.Symlink(target, link); err != nil {
		return err
	}
	return f.root.Lchown(link, f.uid, f.gid)
}

// Link creates a hard link to a file inside the directory.
func (f *FS) Link(from, to string) error {
	from, to = Rel(from), Rel(to)
	if err := f.check(from, false); err != nil {
		return err
	}
	if err := f.check(to, false); err != nil {
		return err
	}
	return f.root.Link(from, to)
}

// ReadFile reads a whole regular file of at most limit bytes.
func (f *FS) ReadFile(name string, limit int64) ([]byte, fs.FileInfo, error) {
	file, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	fi, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if fi.Size() > limit {
		return nil, nil, &fs.PathError{Op: "read", Path: Rel(name), Err: ErrTooLarge}
	}
	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(b)) > limit {
		return nil, nil, &fs.PathError{Op: "read", Path: Rel(name), Err: ErrTooLarge}
	}
	return b, fi, nil
}

// WriteFile creates or replaces a regular file's contents in place (so a
// symlink inside the directory is written through, and the file keeps its
// mode and hard links), creating missing parent directories.
func (f *FS) WriteFile(name string, data []byte) error {
	name = Rel(name)
	if name == "." {
		return &fs.PathError{Op: "write", Path: name, Err: ErrRoot}
	}
	if err := f.MkdirAll(path.Dir(name)); err != nil {
		return err
	}
	file, err := f.Open(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	return err
}

// Copy copies a regular file next to itself as "name copy.ext" (then
// "name copy 2.ext", …) and returns the new name.
func (f *FS) Copy(name string) (string, error) {
	name = Rel(name)
	src, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	fi, err := src.Stat()
	if err != nil {
		return "", err
	}
	dir, base := path.Split(name)
	ext := path.Ext(base)
	if strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1 {
		ext = "" // ".env" has no extension
	}
	stem := strings.TrimSuffix(base, ext)
	for n := 1; n <= 100; n++ {
		suffix := " copy"
		if n > 1 {
			suffix = fmt.Sprintf(" copy %d", n)
		}
		to := dir + stem + suffix + ext
		if f.Denied(to, false) {
			continue
		}
		dst, err := f.Open(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = io.Copy(dst, src)
		if err == nil {
			err = dst.Chmod(fi.Mode().Perm())
		}
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = f.root.Remove(to)
			return "", err
		}
		return to, nil
	}
	return "", &fs.PathError{Op: "copy", Path: name, Err: fs.ErrExist}
}

// Chtimes sets access and modification times.
func (f *FS) Chtimes(name string, atime, mtime time.Time) error {
	name = Rel(name)
	if err := f.check(name, false); err != nil {
		return err
	}
	return f.root.Chtimes(name, atime, mtime)
}
