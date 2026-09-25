package localapi

import (
	"net"
	"syscall"
)

// peerUID returns the Unix user ID of the process on the other end of a Unix
// socket connection (SO_PEERCRED).
func peerUID(c net.Conn) (uint32, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *syscall.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || cerr != nil {
		return 0, false
	}
	return cred.Uid, true
}
