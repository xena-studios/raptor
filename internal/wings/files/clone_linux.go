package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// clone makes out share in's blocks (FICLONE: a reflink, on XFS with
// reflink=1 or btrfs), so a copy is instant and takes no space until either
// changes. It reports false where the filesystem can't, and the caller
// copies the bytes instead.
func clone(out, in *os.File) bool {
	return unix.IoctlFileClone(int(out.Fd()), int(in.Fd())) == nil //nolint:gosec // file descriptors fit in an int
}
