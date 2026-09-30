package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/pkg/sftp"
)

// Servers is what SFTP needs from the server manager.
type Servers interface {
	// Resolve returns the ID of the server a username names: its full ID
	// or its short ID.
	Resolve(ref string) (string, error)
	// FilesDir returns the server's directory.
	FilesDir(id string) (string, error)
	// CheckFiles returns an error while the server's files can't be read,
	// or written if write is set (installing, deleted, over the soft disk
	// limit).
	CheckFiles(ctx context.Context, id string, write bool) error
}

// Modes of what SFTP creates. Wings runs with UMask=0077, so they're set
// explicitly after creating.
const (
	fileMode = 0o644
	dirMode  = 0o755
)

var errNotRegular = errors.New("not a regular file")

// files serves one session's SFTP requests. Every operation goes through an
// os.Root on the server's directory, so nothing outside it can be reached:
// absolute paths, "..", and symlinks pointing out are all refused
// (docs/SECURITY-MODEL.md#wings).
type files struct {
	ctx      context.Context
	root     *os.Root
	serverID string
	grant    Grant
	servers  Servers
	uid, gid int
}

func (h *files) handlers() sftp.Handlers {
	return sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
}

// rel turns a request path (absolute, from the server's directory) into a
// path relative to the root.
func rel(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return p
}

// allow checks the grant and that the server's files are available now.
func (h *files) allow(perm string) error {
	if !h.grant.Has(perm) {
		return sftp.ErrSSHFxPermissionDenied
	}
	return h.servers.CheckFiles(h.ctx, h.serverID, false)
}

// allowGrow is allow(PermWrite) for operations that can use more disk:
// they're refused while the server is over its soft disk limit. Deleting,
// renaming, and truncating stay allowed, so space can be freed.
func (h *files) allowGrow() error {
	if err := h.allow(PermWrite); err != nil {
		return err
	}
	return h.servers.CheckFiles(h.ctx, h.serverID, true)
}

// open opens a regular file. Anything else (a FIFO, a device node a hostile
// install script left behind, a directory) is refused before it's opened for
// real, and checked again on the open file in case it was swapped in between.
// O_NONBLOCK keeps a FIFO swapped in from blocking the open.
func (h *files) open(name string, flag int) (*os.File, error) {
	fi, err := h.root.Stat(name)
	created := errors.Is(err, fs.ErrNotExist) && flag&os.O_CREATE != 0
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return nil, &fs.PathError{Op: "open", Path: name, Err: errNotRegular}
	case err != nil && !created:
		return nil, err
	}
	f, err := h.root.OpenFile(name, flag|syscall.O_NONBLOCK|syscall.O_NOCTTY, fileMode)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil && !st.Mode().IsRegular() {
		err = &fs.PathError{Op: "open", Path: name, Err: errNotRegular}
	}
	if err == nil && created {
		err = f.Chmod(fileMode)
	}
	// New files belong to the server's user, not root.
	if err == nil && (created || owner(st) == 0) {
		err = f.Chown(h.uid, h.gid)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func owner(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// openFlags maps SFTP open flags to os flags. Append is ignored: SFTP
// writes carry their offset, and os.File refuses WriteAt on O_APPEND files.
func openFlags(f sftp.FileOpenFlags) int {
	flag := os.O_WRONLY
	if f.Read {
		flag = os.O_RDWR
	}
	if f.Creat {
		flag |= os.O_CREATE
	}
	if f.Trunc {
		flag |= os.O_TRUNC
	}
	if f.Excl {
		flag |= os.O_EXCL
	}
	return flag
}

// Fileread implements sftp.FileReader.
func (h *files) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if err := h.allow(PermRead); err != nil {
		return nil, err
	}
	return h.open(rel(r.Filepath), os.O_RDONLY)
}

// Filewrite implements sftp.FileWriter.
func (h *files) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if err := h.allowGrow(); err != nil {
		return nil, err
	}
	return h.open(rel(r.Filepath), openFlags(r.Pflags()))
}

// OpenFile implements sftp.OpenFileWriter, for opens that read and write.
func (h *files) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	if err := h.allow(PermRead); err != nil {
		return nil, err
	}
	if err := h.allowGrow(); err != nil {
		return nil, err
	}
	return h.open(rel(r.Filepath), openFlags(r.Pflags()))
}

// Filecmd implements sftp.FileCmder.
func (h *files) Filecmd(r *sftp.Request) error {
	if err := h.allow(PermWrite); err != nil {
		return err
	}
	name := rel(r.Filepath)
	switch r.Method {
	case "Setstat":
		return h.setstat(name, r)
	case "Rename":
		// SFTP v3 rename never replaces: clients that want that use
		// posix-rename.
		if _, err := h.root.Lstat(rel(r.Target)); err == nil {
			return &fs.PathError{Op: "rename", Path: rel(r.Target), Err: fs.ErrExist}
		}
		return h.rename(name, rel(r.Target))
	case "Rmdir":
		if fi, err := h.root.Lstat(name); err != nil {
			return err
		} else if !fi.IsDir() {
			return &fs.PathError{Op: "rmdir", Path: name, Err: syscall.ENOTDIR}
		}
		return h.remove(name)
	case "Remove":
		if fi, err := h.root.Lstat(name); err != nil {
			return err
		} else if fi.IsDir() {
			return &fs.PathError{Op: "remove", Path: name, Err: syscall.EISDIR}
		}
		return h.remove(name)
	case "Mkdir":
		if err := h.allowGrow(); err != nil {
			return err
		}
		if err := h.root.Mkdir(name, dirMode); err != nil {
			return err
		}
		if err := h.root.Chmod(name, dirMode); err != nil {
			return err
		}
		return h.root.Lchown(name, h.uid, h.gid)
	case "Symlink":
		// r.Filepath is the link's target, stored as given. It may point
		// anywhere: Wings never follows a link out of the server's
		// directory, and in the container it resolves inside the container,
		// as a link the server made itself would.
		link := rel(r.Target)
		if err := h.allowGrow(); err != nil {
			return err
		}
		if err := h.root.Symlink(r.Filepath, link); err != nil {
			return err
		}
		return h.root.Lchown(link, h.uid, h.gid)
	case "Link":
		return h.root.Link(name, rel(r.Target))
	}
	return sftp.ErrSSHFxOpUnsupported
}

// PosixRename implements sftp.PosixRenameFileCmder: rename, replacing the
// target.
func (h *files) PosixRename(r *sftp.Request) error {
	if err := h.allow(PermWrite); err != nil {
		return err
	}
	return h.rename(rel(r.Filepath), rel(r.Target))
}

func (h *files) rename(from, to string) error {
	if from == "." || to == "." {
		return sftp.ErrSSHFxPermissionDenied
	}
	return h.root.Rename(from, to)
}

func (h *files) remove(name string) error {
	if name == "." {
		return sftp.ErrSSHFxPermissionDenied
	}
	return h.root.Remove(name)
}

func (h *files) setstat(name string, r *sftp.Request) error {
	flags, attrs := r.AttrFlags(), r.Attributes()
	if flags.Size {
		f, err := h.open(name, os.O_WRONLY)
		if err != nil {
			return err
		}
		err = f.Truncate(int64(attrs.Size)) //nolint:gosec // an oversized value fails in the kernel
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	if flags.Permissions {
		// Only permission bits: never setuid, setgid, or sticky.
		if err := h.root.Chmod(name, attrs.FileMode().Perm()); err != nil {
			return err
		}
	}
	if flags.Acmodtime {
		if err := h.root.Chtimes(name, attrs.AccessTime(), attrs.ModTime()); err != nil {
			return err
		}
	}
	// Owner changes are ignored: files always belong to the server's user.
	// Clients that preserve ownership send them on every upload, so refusing
	// would break uploads.
	return nil
}

// Filelist implements sftp.FileLister.
func (h *files) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if err := h.allow(PermRead); err != nil {
		return nil, err
	}
	name := rel(r.Filepath)
	switch r.Method {
	case "List":
		return h.list(name)
	case "Stat":
		fi, err := h.root.Stat(name)
		if err != nil {
			return nil, err
		}
		return listerAt{fi}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

// Lstat implements sftp.LstatFileLister.
func (h *files) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	if err := h.allow(PermRead); err != nil {
		return nil, err
	}
	fi, err := h.root.Lstat(rel(r.Filepath))
	if err != nil {
		return nil, err
	}
	return listerAt{fi}, nil
}

// Readlink implements sftp.ReadlinkFileLister.
func (h *files) Readlink(p string) (string, error) {
	if err := h.allow(PermRead); err != nil {
		return "", err
	}
	return h.root.Readlink(rel(p))
}

// RealPath implements sftp.RealPathFileLister. It's lexical: the result
// is where the client will send requests, which rel confines anyway.
func (h *files) RealPath(p string) (string, error) {
	return path.Clean("/" + p), nil
}

// list reads a directory. O_DIRECTORY makes opening anything else fail
// right away (a FIFO would otherwise block).
func (h *files) list(name string) (sftp.ListerAt, error) {
	f, err := h.root.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make(listerAt, 0, len(entries))
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

type listerAt []fs.FileInfo

func (l listerAt) ListAt(dst []fs.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}
