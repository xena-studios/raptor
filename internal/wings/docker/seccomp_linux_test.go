package docker

import (
	"math/rand/v2"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// match evaluates the allow rules the way seccomp does.
func allowed(rules [][]specs.LinuxSeccompArg, v uint64) bool {
	for _, r := range rules {
		a := r[0]
		switch a.Op {
		case specs.OpLessThan:
			if v < a.Value {
				return true
			}
		case specs.OpMaskedEqual:
			if v&a.Value == a.ValueTwo {
				return true
			}
		}
	}
	return false
}

func TestIoctlRules(t *testing.T) {
	rules := ioctlAllowArgs(fsIocFsSetXattr)
	t.Logf("%d allow rules", len(rules))
	for _, c := range []struct {
		v    uint64
		want bool
		name string
	}{
		{fsIocFsSetXattr, false, "FS_IOC_FSSETXATTR"},
		{0xffffffff00000000 | fsIocFsSetXattr, false, "FS_IOC_FSSETXATTR with high bits"},
		{0xdeadbeef00000000 | fsIocFsSetXattr, false, "FS_IOC_FSSETXATTR with other high bits"},
		{0x5401, true, "TCGETS"},
		{0x801c581f, true, "FS_IOC_FSGETXATTR"},
		{0xffffffff801c581f, true, "FS_IOC_FSGETXATTR sign-extended (musl)"},
		{0x80086601, true, "FS_IOC_GETFLAGS"},
		{fsIocFsSetXattr - 1, true, "just below"},
		{fsIocFsSetXattr + 1, true, "just above"},
		{0xffffffff, true, "max 32-bit"},
		{0x100000000, false, "33-bit value (never legitimate)"},
	} {
		if got := allowed(rules, c.v); got != c.want {
			t.Errorf("%s (%#x): allowed=%v, want %v", c.name, c.v, got, c.want)
		}
	}
	// Exhaustively around the blocked value, and randomly everywhere else:
	// allowed iff the low 32 bits aren't the blocked value and the value is
	// either a 32-bit value or sign-extended.
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 2_000_000 {
		var v uint64
		switch {
		case i < 1_000_000:
			v = fsIocFsSetXattr - 500_000 + uint64(i)
		case i%2 == 0:
			v = uint64(r.Uint32())
		default:
			v = r.Uint64()
		}
		legit := v <= 0xffffffff || v&0xffffffff80000000 == 0xffffffff80000000
		want := legit && v&0xffffffff != fsIocFsSetXattr
		if allowed(rules, v) != want {
			t.Fatalf("%#x: allowed=%v, want %v", v, !want, want)
		}
	}
}
