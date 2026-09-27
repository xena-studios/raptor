package docker

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/moby/profiles/seccomp"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// fsIocFsSetXattr is FS_IOC_FSSETXATTR (also XFS_IOC_FSSETXATTR): it can
// move a file into another XFS quota project. A file's owner may call it
// without any capability, so without this rule any container could move its
// files out of its disk quota and fill the volume (docs/WINGS.md#disk-quotas).
const fsIocFsSetXattr = 0x401c5820

var (
	profileOnce sync.Once
	profileJSON string
)

// SeccompProfile is the seccomp profile for every Raptor container: Docker's
// default profile, except that ioctl(FS_IOC_FSSETXATTR) is refused.
//
// Two details make this harder than one deny rule:
//   - libseccomp lets a matching allow rule beat a conditional deny, so
//     ioctl is removed from the unconditional allowlist and re-allowed only
//     for command values that can't be FS_IOC_FSSETXATTR.
//   - The kernel truncates the ioctl command to 32 bits, so a value with
//     arbitrary high bits set must never match an allow rule, or it would
//     reach the kernel as FS_IOC_FSSETXATTR. Values sign-extended from a
//     negative 32-bit int are allowed, because musl passes commands as int
//     and legitimate commands with the top bit set arrive that way.
//
// (file_setattr(2), the newer way to change a file's project, isn't in
// Docker's allowlist, so it's already refused.)
func SeccompProfile() string {
	profileOnce.Do(func() {
		p := seccomp.DefaultProfile()
		for _, s := range p.Syscalls {
			if s.Action == specs.ActAllow && len(s.Args) == 0 {
				s.Names = slices.DeleteFunc(s.Names, func(n string) bool { return n == "ioctl" })
			}
		}
		for _, args := range ioctlAllowArgs(fsIocFsSetXattr) {
			p.Syscalls = append(p.Syscalls, &seccomp.Syscall{
				LinuxSyscall: specs.LinuxSyscall{Names: []string{"ioctl"}, Action: specs.ActAllow, Args: args},
				Comment:      "Raptor: every ioctl except FS_IOC_FSSETXATTR",
			})
		}
		b, err := json.Marshal(p)
		if err != nil {
			panic(err) // the profile is static data
		}
		profileJSON = string(b)
	})
	return profileJSON
}

// ioctlAllowArgs returns conditions (one rule each) that together match every
// ioctl command a program can legitimately pass, except blocked:
//
//   - below blocked (always a 32-bit value);
//   - above blocked but within 32 bits, as bit-prefix matches that also
//     require the high 32 bits to be zero;
//   - sign-extended negative values (high 33 bits all ones), which blocked
//     can't be because its top bit is 0.
func ioctlAllowArgs(blocked uint64) [][]specs.LinuxSeccompArg {
	const hi = 0xffffffff00000000
	rules := [][]specs.LinuxSeccompArg{
		{{Index: 1, Value: blocked, Op: specs.OpLessThan}},
		{{Index: 1, Value: 0xffffffff80000000, ValueTwo: 0xffffffff80000000, Op: specs.OpMaskedEqual}},
	}
	// Cover [blocked+1, 0xffffffff] with aligned blocks, each one a masked
	// match on its prefix (like splitting an IP range into CIDR blocks).
	lo, top := blocked+1, uint64(0xffffffff)
	for lo <= top {
		size := uint64(1)
		for lo%(size*2) == 0 && lo+size*2-1 <= top {
			size *= 2
		}
		mask := hi | (0xffffffff &^ (size - 1))
		rules = append(rules, []specs.LinuxSeccompArg{{Index: 1, Value: mask, ValueTwo: lo, Op: specs.OpMaskedEqual}})
		lo += size
	}
	return rules
}
