//go:build !linux

package localapi

import "net"

// peerUID is only supported on Linux, where Wings runs.
func peerUID(net.Conn) (uint32, bool) { return 0, false }
