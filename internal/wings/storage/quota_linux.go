// Package storage manages the volume server data lives on and its XFS
// project quotas (docs/WINGS.md#disk-quotas): one quota project per server,
// enforced by the kernel, with usage read instantly from the quota instead
// of walking files.
package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Kernel ABI: linux/fs.h (struct fsxattr) and linux/dqblk_xfs.h
// (struct fs_disk_quota). Sizes are checked in tests.
const (
	fsIocFsGetXattr    = 0x801c581f // _IOR('X', 31, struct fsxattr)
	fsIocFsSetXattr    = 0x401c5820 // _IOW('X', 32, struct fsxattr)
	fsXflagProjInherit = 0x00000200

	prjQuota      = 2      // PRJQUOTA
	qXGetQuota    = 0x5803 // Q_XGETQUOTA = XQM_CMD(3)
	qXSetQLim     = 0x5804 // Q_XSETQLIM = XQM_CMD(4)
	fsDquotVer    = 1      // FS_DQUOT_VERSION
	fsProjQuota   = 2      // FS_PROJ_QUOTA
	fsDqBHard     = 1 << 3 // FS_DQ_BHARD
	fsDqBSoft     = 1 << 2 // FS_DQ_BSOFT
	fsDqIHard     = 1 << 1 // FS_DQ_IHARD
	basicBlock    = 512    // quota block counts are in 512-byte "basic blocks"
	sysQuotactlFd = 443    // quotactl_fd(2), the same on amd64 and arm64
)

type fsxattr struct {
	Xflags     uint32
	Extsize    uint32
	Nextents   uint32
	Projid     uint32
	Cowextsize uint32
	Pad        [8]byte
}

type fsDiskQuota struct {
	Version    int8
	Flags      int8
	Fieldmask  uint16
	ID         uint32
	BlkHard    uint64
	BlkSoft    uint64
	InoHard    uint64
	InoSoft    uint64
	BCount     uint64
	ICount     uint64
	ITimer     int32
	BTimer     int32
	IWarns     uint16
	BWarns     uint16
	ITimerHi   int8
	BTimerHi   int8
	RtbTimerHi int8
	Padding2   int8
	RtbHard    uint64
	RtbSoft    uint64
	RtbCount   uint64
	RtbTimer   int32
	RtbWarns   uint16
	Padding3   int16
	Padding4   [8]byte
}

// qcmd is QCMD(cmd, type).
func qcmd(cmd, typ uint32) uintptr { return uintptr(cmd<<8 | typ&0xff) }

func quotactlFd(fd uintptr, cmd uintptr, id uint32, addr unsafe.Pointer) error {
	_, _, e := unix.Syscall6(sysQuotactlFd, fd, cmd, uintptr(id), uintptr(addr), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

// SetProject assigns a directory to a quota project and marks it so that
// everything created inside inherits the project. The directory should be
// empty (existing files keep their project; see ApplyProject).
func SetProject(dir string, project uint32) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0) //nolint:gosec // Wings' own server directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return setProjectFd(f.Fd(), project, true)
}

func setProjectFd(fd uintptr, project uint32, inherit bool) error {
	var x fsxattr
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, fsIocFsGetXattr, uintptr(unsafe.Pointer(&x))); e != 0 { //nolint:gosec // fsxattr ABI struct, size checked by tests
		return fmt.Errorf("get project: %w", e)
	}
	x.Projid = project
	if inherit {
		x.Xflags |= fsXflagProjInherit
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, fsIocFsSetXattr, uintptr(unsafe.Pointer(&x))); e != 0 { //nolint:gosec // fsxattr ABI struct, size checked by tests
		return fmt.Errorf("set project: %w", e)
	}
	return nil
}

// Project returns a file's quota project.
func Project(path string) (uint32, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0) //nolint:gosec // a file inside Wings' own server directory
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var x fsxattr
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFsGetXattr, uintptr(unsafe.Pointer(&x))); e != 0 { //nolint:gosec // fsxattr ABI struct, size checked by tests
		return 0, e
	}
	return x.Projid, nil
}

// SetLimit sets a project's hard limit in bytes on the filesystem mounted at
// mountpoint. 0 removes the limit.
func SetLimit(mountpoint string, project uint32, bytes int64) error {
	if bytes < 0 {
		return errors.New("negative limit")
	}
	f, err := os.Open(mountpoint) //nolint:gosec // Wings' own volume path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	blocks := uint64(bytes+basicBlock-1) / basicBlock
	q := fsDiskQuota{
		Version: fsDquotVer, Flags: fsProjQuota, ID: project,
		Fieldmask: fsDqBHard | fsDqBSoft,
		BlkHard:   blocks, BlkSoft: blocks,
	}
	if err := quotactlFd(f.Fd(), qcmd(qXSetQLim, prjQuota), project, unsafe.Pointer(&q)); err != nil { //nolint:gosec // fs_disk_quota ABI struct, size checked by tests
		return fmt.Errorf("set quota for project %d: %w", project, err)
	}
	return nil
}

// Usage is a project's current usage and limit.
type Usage struct {
	Bytes      int64
	Inodes     int64
	LimitBytes int64 // 0 = unlimited
}

// GetUsage reads a project's usage straight from the quota (no file walk).
func GetUsage(mountpoint string, project uint32) (Usage, error) {
	f, err := os.Open(mountpoint) //nolint:gosec // Wings' own volume path
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = f.Close() }()
	var q fsDiskQuota
	if err := quotactlFd(f.Fd(), qcmd(qXGetQuota, prjQuota), project, unsafe.Pointer(&q)); err != nil { //nolint:gosec // fs_disk_quota ABI struct, size checked by tests
		if errors.Is(err, unix.ENOENT) {
			return Usage{}, nil // no usage and no limit recorded yet
		}
		return Usage{}, fmt.Errorf("read quota for project %d: %w", project, err)
	}
	// Kernel counters of 512-byte blocks: far below int64 range.
	return Usage{
		Bytes:      int64(q.BCount) * basicBlock,  //nolint:gosec // see above
		Inodes:     int64(q.ICount),               //nolint:gosec // see above
		LimitBytes: int64(q.BlkHard) * basicBlock, //nolint:gosec // see above
	}, nil
}

// ApplyProject moves a directory tree into a quota project: every directory
// gets the project and the inherit flag, every file the project. Symlinks
// are skipped (their targets are handled where they live) and the walk never
// leaves dir.
func ApplyProject(dir string, project uint32) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		f, err := root.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		err = setProjectFd(f.Fd(), project, d.IsDir())
		_ = f.Close()
		return err
	})
}
