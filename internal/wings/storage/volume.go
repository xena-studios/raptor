package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ErrUnavailable means server data can't be written safely: the volume isn't
// mounted (servers would write into the empty directory underneath, onto the
// host's disk, without limits) or doesn't enforce project quotas.
var ErrUnavailable = errors.New("server data volume is unavailable")

// Volume is where server data lives (docs/WINGS.md#disk-quotas).
type Volume struct {
	// Path is the volumes directory, e.g. /var/lib/raptor/volumes.
	Path string
	// Soft is quota tier 3: the owner opted out of quotas (storage.quotas:
	// false). Limits are checked by scanning instead of enforced by the
	// kernel, and no mount is required.
	Soft bool
}

// Check reports whether server data can be written. It reads the mount
// table each time (cheap), so a volume that disappears is caught before the
// next start or install, not after.
func (v *Volume) Check() error {
	if v.Soft {
		if fi, err := os.Stat(v.Path); err != nil || !fi.IsDir() {
			return fmt.Errorf("%w: %s doesn't exist", ErrUnavailable, v.Path)
		}
		return nil
	}
	m, ok, err := FindMount(v.Path)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	case !ok:
		return fmt.Errorf("%w: nothing is mounted at %s (run `raptor storage status`)", ErrUnavailable, v.Path)
	case !m.HasProjectQuota():
		return fmt.Errorf("%w: %s is %s without project quotas (it must be XFS mounted with prjquota)", ErrUnavailable, v.Path, m.FSType)
	}
	return nil
}

// Prepare creates a server's directory (if needed) and puts it under its
// quota project with its limit. It's idempotent and safe to call before
// every install and start. Existing files are moved into the project too, so
// a server created before quotas (or imported) is covered.
func (v *Volume) Prepare(dir string, project uint32, limitBytes int64, uid, gid int) error {
	if err := v.Check(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the server's own directory
		return err
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return err
	}
	if v.Soft {
		return nil
	}
	cur, err := Project(dir)
	if err != nil {
		return err
	}
	if cur != project {
		if err := ApplyProject(dir, project); err != nil {
			return fmt.Errorf("quota project: %w", err)
		}
	}
	return SetLimit(v.Path, project, limitBytes)
}

// SetLimit changes a server's limit. With quotas it takes effect instantly,
// even while the server runs.
func (v *Volume) SetLimit(project uint32, limitBytes int64) error {
	if v.Soft {
		return nil
	}
	return SetLimit(v.Path, project, limitBytes)
}

// Release clears a deleted server's limit.
func (v *Volume) Release(project uint32) error {
	if v.Soft || project == 0 {
		return nil
	}
	return SetLimit(v.Path, project, 0)
}

// Usage returns a server's disk usage: instantly from the quota, or by
// scanning its directory in soft mode.
func (v *Volume) Usage(ctx context.Context, dir string, project uint32) (Usage, error) {
	if !v.Soft {
		return GetUsage(v.Path, project)
	}
	return Scan(ctx, dir)
}

// Scan adds up a directory's disk usage (allocated blocks, like du) without
// following symlinks or leaving the directory.
func Scan(ctx context.Context, dir string) (Usage, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = root.Close() }()
	var u Usage
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		u.Inodes++
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			u.Bytes += st.Blocks * 512
		} else {
			u.Bytes += info.Size()
		}
		return nil
	})
	return u, err
}
