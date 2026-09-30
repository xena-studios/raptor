package sftp

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"

	"github.com/pkg/sftp"

	wfiles "github.com/xena-studios/raptor/internal/wings/files"
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
	// Denylist returns the file_denylist of the server's egg.
	Denylist(id string) ([]string, error)
}

// files serves one session's SFTP requests through files.FS: an os.Root on
// the server's directory, so nothing outside it can be reached (absolute
// paths, "..", and symlinks pointing out are all refused), with the egg's
// denylist enforced (docs/SECURITY-MODEL.md#wings).
type files struct {
	ctx      context.Context
	fs       *wfiles.FS
	serverID string
	grant    Grant
	servers  Servers
}

func (h *files) handlers() sftp.Handlers {
	return sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
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
	return h.fs.Open(r.Filepath, os.O_RDONLY)
}

// Filewrite implements sftp.FileWriter.
func (h *files) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if err := h.allowGrow(); err != nil {
		return nil, err
	}
	return h.fs.Open(r.Filepath, openFlags(r.Pflags()))
}

// OpenFile implements sftp.OpenFileWriter, for opens that read and write.
func (h *files) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	if err := h.allow(PermRead); err != nil {
		return nil, err
	}
	if err := h.allowGrow(); err != nil {
		return nil, err
	}
	return h.fs.Open(r.Filepath, openFlags(r.Pflags()))
}

// Filecmd implements sftp.FileCmder.
func (h *files) Filecmd(r *sftp.Request) error {
	if err := h.allow(PermWrite); err != nil {
		return err
	}
	name := wfiles.Rel(r.Filepath)
	switch r.Method {
	case "Setstat":
		return h.setstat(name, r)
	case "Rename":
		// SFTP v3 rename never replaces: clients that want that use
		// posix-rename.
		return h.fs.Rename(name, r.Target, false)
	case "Rmdir":
		if fi, err := h.fs.Lstat(name); err != nil {
			return err
		} else if !fi.IsDir() {
			return &fs.PathError{Op: "rmdir", Path: name, Err: syscall.ENOTDIR}
		}
		return h.fs.Remove(name)
	case "Remove":
		if fi, err := h.fs.Lstat(name); err != nil {
			return err
		} else if fi.IsDir() {
			return &fs.PathError{Op: "remove", Path: name, Err: syscall.EISDIR}
		}
		return h.fs.Remove(name)
	case "Mkdir":
		if err := h.allowGrow(); err != nil {
			return err
		}
		return h.fs.Mkdir(name)
	case "Symlink":
		// r.Filepath is the link's target, stored as given.
		if err := h.allowGrow(); err != nil {
			return err
		}
		return h.fs.Symlink(r.Filepath, r.Target)
	case "Link":
		return h.fs.Link(name, r.Target)
	}
	return sftp.ErrSSHFxOpUnsupported
}

// PosixRename implements sftp.PosixRenameFileCmder: rename, replacing the
// target.
func (h *files) PosixRename(r *sftp.Request) error {
	if err := h.allow(PermWrite); err != nil {
		return err
	}
	return h.fs.Rename(r.Filepath, r.Target, true)
}

func (h *files) setstat(name string, r *sftp.Request) error {
	flags, attrs := r.AttrFlags(), r.Attributes()
	if flags.Size {
		f, err := h.fs.Open(name, os.O_WRONLY)
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
		if err := h.fs.Chmod(name, attrs.FileMode()); err != nil {
			return err
		}
	}
	if flags.Acmodtime {
		if err := h.fs.Chtimes(name, attrs.AccessTime(), attrs.ModTime()); err != nil {
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
	switch r.Method {
	case "List":
		l, err := h.fs.List(r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt(l), nil
	case "Stat":
		fi, err := h.fs.Stat(r.Filepath)
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
	fi, err := h.fs.Lstat(r.Filepath)
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
	return h.fs.Readlink(p)
}

// RealPath implements sftp.RealPathFileLister. It's lexical: the result
// is where the client will send requests, which files.Rel confines anyway.
func (h *files) RealPath(p string) (string, error) {
	return path.Clean("/" + p), nil
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
