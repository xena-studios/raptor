package files

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// setTimes sets an open file's access and modification times, without
// looking its name up again: futimens(2), which is utimensat(2) with no
// path.
func setTimes(f *os.File, atime, mtime time.Time) error {
	ts := [2]unix.Timespec{unix.NsecToTimespec(atime.UnixNano()), unix.NsecToTimespec(mtime.UnixNano())}
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno unix.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall6(unix.SYS_UTIMENSAT, fd, 0, uintptr(unsafe.Pointer(&ts)), 0, 0, 0) //nolint:gosec // the kernel's ABI
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
