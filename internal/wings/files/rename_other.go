//go:build !linux

package files

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// renameat is for development off Linux; without renameat2, noReplace is
// checked first, which is racy but fine there.
func renameat(from *os.File, fromName string, to *os.File, toName string, noReplace bool) error {
	if noReplace {
		var st unix.Stat_t
		if unix.Fstatat(int(to.Fd()), toName, &st, unix.AT_SYMLINK_NOFOLLOW) == nil { //nolint:gosec // file descriptors fit an int
			return fs.ErrExist
		}
	}
	return unix.Renameat(int(from.Fd()), fromName, int(to.Fd()), toName) //nolint:gosec // file descriptors fit an int
}
