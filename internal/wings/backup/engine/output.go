package engine

import (
	"context"
	"io"
	"os"

	kfs "github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/restore"
)

// rootOutput restores a snapshot into an empty directory through an
// os.Root. Files are created exclusively (never through an existing entry),
// owned by the server's user, and lose setuid, setgid, and sticky bits: a
// backup can't plant anything the game couldn't have written itself.
type rootOutput struct {
	root     *os.Root
	uid, gid int // -1: keep the owner (tests)
}

var _ restore.Output = (*rootOutput)(nil)

func (o *rootOutput) Parallelizable() bool { return true }

func (o *rootOutput) BeginDirectory(_ context.Context, rel string, _ kfs.Directory) error {
	if rel == "" {
		return nil // the server directory itself is left as it is
	}
	return o.root.Mkdir(rel, 0o700)
}

func (o *rootOutput) FinishDirectory(_ context.Context, rel string, e kfs.Directory) error {
	if rel == "" {
		return nil
	}
	return o.finish(rel, e)
}

func (o *rootOutput) WriteDirEntry(context.Context, string, *snapshot.DirEntry, kfs.Directory) error {
	return nil
}

func (o *rootOutput) WriteFile(ctx context.Context, rel string, e kfs.File, progress restore.FileWriteProgress) error {
	r, err := e.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	w, err := o.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, &progressReader{r: r, cb: progress})
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return o.finish(rel, e)
}

func (o *rootOutput) finish(rel string, e kfs.Entry) error {
	if err := o.root.Chmod(rel, e.Mode().Perm()); err != nil {
		return err
	}
	if o.uid >= 0 {
		if err := o.root.Lchown(rel, o.uid, o.gid); err != nil {
			return err
		}
	}
	return o.root.Chtimes(rel, e.ModTime(), e.ModTime())
}

func (o *rootOutput) FileExists(context.Context, string, kfs.File) bool { return false }

func (o *rootOutput) CreateSymlink(ctx context.Context, rel string, e kfs.Symlink) error {
	target, err := e.Readlink(ctx)
	if err != nil {
		return err
	}
	if err := o.root.Symlink(target, rel); err != nil {
		return err
	}
	if o.uid >= 0 {
		return o.root.Lchown(rel, o.uid, o.gid)
	}
	return nil
}

func (o *rootOutput) SymlinkExists(context.Context, string, kfs.Symlink) bool { return false }

func (o *rootOutput) Close(context.Context) error { return nil }

type progressReader struct {
	r  io.Reader
	cb restore.FileWriteProgress
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.cb != nil {
		p.cb(int64(n))
	}
	return n, err
}
