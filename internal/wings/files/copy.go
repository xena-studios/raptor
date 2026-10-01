package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// CopyResult says what CopyTree did.
type CopyResult struct {
	Files   int   `json:"files"`
	Bytes   int64 `json:"bytes"`
	Skipped int   `json:"skipped,omitempty"` // FIFOs, devices, sockets
}

// CopyTree copies everything in src (a directory from another panel, read
// through its own os.Root) into the FS: directories, regular files (their
// permission bits and times, never setuid), and symlinks as links. Special
// files are skipped. Nothing is followed out of src or written out of the
// FS, and everything belongs to the FS's user.
func (f *FS) CopyTree(ctx context.Context, src *os.Root, progress func(CopyResult)) (CopyResult, error) {
	var res CopyResult
	err := fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == "." {
			return nil
		}
		fi, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed meanwhile
		}
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if err := f.MkdirAll(p); err != nil {
				return err
			}
			_ = f.root.Chmod(p, fi.Mode().Perm())
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := src.Readlink(p)
			if err != nil {
				return err
			}
			if err := f.Symlink(target, p); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case fi.Mode().IsRegular():
			n, err := f.copyFile(src, p, fi)
			if err != nil {
				return err
			}
			res.Bytes += n
		default:
			res.Skipped++
			return nil
		}
		res.Files++
		if progress != nil && res.Files%1000 == 0 {
			progress(res)
		}
		return nil
	})
	if err == nil {
		// Directories' times, last: writing into them changed them.
		err = fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if fi, ierr := d.Info(); ierr == nil {
					_ = f.root.Chtimes(p, fi.ModTime(), fi.ModTime())
				}
			}
			return nil
		})
	}
	return res, err
}

func (f *FS) copyFile(src *os.Root, p string, fi fs.FileInfo) (int64, error) {
	in, err := src.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() {
		return 0, errors.Join(&fs.PathError{Op: "copy", Path: p, Err: ErrNotRegular}, err)
	}
	out, err := f.Open(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Chmod(fi.Mode().Perm())
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	_ = f.root.Chtimes(p, fi.ModTime(), fi.ModTime())
	return n, nil
}
