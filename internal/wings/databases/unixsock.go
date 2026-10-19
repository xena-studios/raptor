package databases

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// maxSocketPath is how long a Unix socket's path may be (sun_path is 108
// bytes, with its NUL).
const maxSocketPath = 107

// dialUnix connects to a Unix socket, however long its path: one too
// long for the kernel is reached through its folder's descriptor, as
// /proc/self/fd/<n>/<name>, which is always short. Instances' sockets are
// deep in the data volume (mongodb-27017.sock under a UUID), so a volume
// at a long path would otherwise make them unreachable.
func dialUnix(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	if len(path) <= maxSocketPath {
		return d.DialContext(ctx, "unix", path)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	return d.DialContext(ctx, "unix", "/proc/self/fd/"+strconv.Itoa(int(dir.Fd()))+"/"+filepath.Base(path))
}

// unixDialer is dialUnix for drivers that take a dialer.
type unixDialer struct{ path string }

func (u unixDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return dialUnix(ctx, u.path)
}
