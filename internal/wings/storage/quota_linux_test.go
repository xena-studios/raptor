package storage

import (
	"testing"
	"unsafe"
)

// The structs must match the kernel's exactly, or ioctls and quotactl would
// read and write garbage.
func TestABISizes(t *testing.T) {
	if s := unsafe.Sizeof(fsxattr{}); s != 28 {
		t.Errorf("fsxattr is %d bytes, kernel's is 28", s)
	}
	if s := unsafe.Sizeof(fsDiskQuota{}); s != 112 {
		t.Errorf("fs_disk_quota is %d bytes, kernel's is 112", s)
	}
	if o := unsafe.Offsetof(fsDiskQuota{}.RtbHard); o != 72 {
		t.Errorf("d_rtb_hardlimit at offset %d, kernel's is 72", o)
	}
	if o := unsafe.Offsetof(fsxattr{}.Projid); o != 12 {
		t.Errorf("fsx_projid at offset %d, kernel's is 12", o)
	}
	if c := qcmd(qXSetQLim, prjQuota); c != 0x580402 {
		t.Errorf("QCMD(Q_XSETQLIM, PRJQUOTA) = %#x", c)
	}
}
