//go:build !linux

package files

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// setTimes sets an open file's access and modification times (futimes off
// Linux, for development).
func setTimes(f *os.File, atime, mtime time.Time) error {
	tv := []unix.Timeval{unix.NsecToTimeval(atime.UnixNano()), unix.NsecToTimeval(mtime.UnixNano())}
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = unix.Futimes(int(fd), tv) }); err != nil { //nolint:gosec // file descriptors fit an int
		return err
	}
	return serr
}
