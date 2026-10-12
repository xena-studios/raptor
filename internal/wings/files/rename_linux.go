package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameat moves a name between two open directories; with noReplace, an
// existing target is refused (fs.ErrExist) by the kernel itself.
func renameat(from *os.File, fromName string, to *os.File, toName string, noReplace bool) error {
	var flags uint
	if noReplace {
		flags = unix.RENAME_NOREPLACE
	}
	return unix.Renameat2(int(from.Fd()), fromName, int(to.Fd()), toName, flags) //nolint:gosec // file descriptors fit an int
}
