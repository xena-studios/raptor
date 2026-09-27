//go:build e2e

// Validation gate for disk quotas (docs/ROADMAP.md 1.6), run as root against
// a real XFS loop volume mounted with prjquota, by scripts/e2e-quotas.sh
// (task e2e:quotas locally, and CI).
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"

	wingsdocker "github.com/xena-studios/raptor/internal/wings/docker"
)

const mib = 1 << 20

// TestMain doubles as a helper that runs inside containers.
func TestMain(m *testing.M) {
	switch os.Getenv("RAPTOR_E2E_HELPER") {
	case "escape":
		os.Exit(escapeHelper(os.Args[len(os.Args)-1]))
	case "fill":
		os.Exit(fillHelper(os.Args[len(os.Args)-1]))
	}
	os.Exit(m.Run())
}

// fill writes until the disk refuses, and reports how far it got.
func fillHelper(dir string) int {
	n, err := fill(filepath.Join(dir, "fill.bin"), 300*mib)
	fmt.Printf("WROTE %d MiB, err=%v\n", n/mib, err)
	return 0
}

// escapeHelper tries to move a file out of its quota project (to project 0),
// with the plain ioctl and with the high bits of the command set (the kernel
// truncates the command to 32 bits; a seccomp rule comparing all 64 would
// miss it), and then writes past the limit.
func escapeHelper(dir string) int {
	path := filepath.Join(dir, "escape.bin")
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("CREATE", err)
		return 0
	}
	err = setProjectFd(f.Fd(), 0, false)
	fmt.Printf("SETPROJECT err=%v\n", err)
	var x fsxattr
	_, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFsSetXattr|0xffffffff00000000, uintptr(unsafe.Pointer(&x)))
	fmt.Printf("SETPROJECT-HIGHBITS err=%v\n", errnoOrNil(e))
	_ = f.Close()
	p, _ := Project(path)
	fmt.Printf("PROJECT %d\n", p)
	n, err := fill(path, 300*mib)
	fmt.Printf("WROTE %d MiB, err=%v\n", n/mib, err)
	return 0
}

func errnoOrNil(e unix.Errno) error {
	if e == 0 {
		return nil
	}
	return e
}

func fill(path string, max int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644) //nolint:gosec // test file
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, mib)
	var n int64
	for n < max {
		w, err := f.Write(buf)
		n += int64(w)
		if err != nil {
			return n, err
		}
	}
	return n, f.Sync()
}

func volume(t *testing.T) string {
	v := os.Getenv("RAPTOR_E2E_VOLUME")
	if v == "" {
		t.Skip("RAPTOR_E2E_VOLUME not set")
	}
	return v
}

func projectDir(t *testing.T, vol string, project uint32, limit int64, owner ...int) string {
	t.Helper()
	dir := filepath.Join(vol, fmt.Sprintf("gate-%d", project))
	_ = os.RemoveAll(dir)
	if err := os.Mkdir(dir, 0o777); err != nil { //nolint:gosec // test dir writable by the container user
		t.Fatal(err)
	}
	if err := SetProject(dir, project); err != nil {
		t.Fatal(err)
	}
	if len(owner) == 1 {
		if err := os.Chown(dir, owner[0], owner[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetLimit(vol, project, limit); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
		_ = SetLimit(vol, project, 0)
	})
	return dir
}

func TestGateHostLimit(t *testing.T) {
	vol := volume(t)
	dir := projectDir(t, vol, 5001, 100*mib)
	// XFS reports a full project quota as ENOSPC (the project behaves like
	// a smaller filesystem), not EDQUOT.
	n, err := fill(filepath.Join(dir, "big.bin"), 150*mib)
	if !errors.Is(err, unix.ENOSPC) && !errors.Is(err, unix.EDQUOT) {
		t.Fatalf("root wrote %d MiB past a 100 MiB limit (err %v)", n/mib, err)
	}
	u, err := GetUsage(vol, 5001)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("root on the host: stopped at %d MiB (%v); quota reports %d MiB used of %d MiB, %d inodes", n/mib, err, u.Bytes/mib, u.LimitBytes/mib, u.Inodes)
	if u.Bytes < 99*mib || u.Bytes > 101*mib || u.LimitBytes != 100*mib {
		t.Fatalf("usage %+v", u)
	}
	// Limits change instantly.
	if err := SetLimit(vol, 5001, 120*mib); err != nil {
		t.Fatal(err)
	}
	n2, err := fill(filepath.Join(dir, "more.bin"), 50*mib)
	if (!errors.Is(err, unix.ENOSPC) && !errors.Is(err, unix.EDQUOT)) || n2 < 19*mib || n2 > 21*mib {
		t.Fatalf("after raising to 120 MiB, wrote %d MiB more (err %v), want ~20", n2/mib, err)
	}
	// New files inherit the project.
	if p, err := Project(filepath.Join(dir, "more.bin")); err != nil || p != 5001 {
		t.Fatalf("new file's project = %d, %v", p, err)
	}
}

// docker runs the test binary inside a BusyBox container with the given
// Docker flags, bind-mounting dir at /data.
func docker(t *testing.T, dir, helper string, flags ...string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"run", "--rm", "-e", "RAPTOR_E2E_HELPER=" + helper, "-v", self + ":/helper:ro", "-v", dir + ":/data"}, flags...)
	args = append(args, "busybox:1", "/helper", "/data")
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Root inside a container (as install scripts run) is limited too.
func TestGateContainerLimit(t *testing.T) {
	vol := volume(t)
	dir := projectDir(t, vol, 5002, 100*mib)
	out := docker(t, dir, "fill", "--cap-drop", "NET_RAW", "--cap-drop", "MKNOD", "--cap-drop", "AUDIT_WRITE", "--cap-drop", "SETFCAP")
	t.Logf("root in an install-like container: %s", out)
	if !strings.Contains(out, "WROTE 100 MiB") && !strings.Contains(out, "WROTE 99 MiB") {
		t.Fatalf("container root wasn't stopped at the limit: %s", out)
	}
}

// A container must not be able to move its files out of the quota project:
// neither root in an install container nor the server's own user (who owns
// its files, and file owners may change a file's project).
func TestGateEscape(t *testing.T) {
	vol := volume(t)
	for _, c := range []struct {
		name  string
		owner []int
		flags []string
	}{
		{"install container (root)", nil, append([]string{"--cap-drop", "NET_RAW", "--cap-drop", "MKNOD", "--cap-drop", "AUDIT_WRITE", "--cap-drop", "SETFCAP"}, securityFlags()...)},
		{"server container (uid 988, owns its files)", []int{988}, append([]string{"--user", "988:988", "--cap-drop", "FOWNER", "--cap-drop", "DAC_OVERRIDE"}, securityFlags()...)},
	} {
		dir := projectDir(t, vol, 5003, 100*mib, c.owner...)
		out := docker(t, dir, "escape", c.flags...)
		t.Logf("%s:\n%s", c.name, out)
		if !strings.Contains(out, "PROJECT 5003") || strings.Contains(out, "WROTE 300 MiB") {
			t.Errorf("%s escaped its quota", c.name)
		}
	}
}

// securityFlags runs the container with Wings' seccomp profile, as Wings does.
func securityFlags() []string {
	f, err := os.CreateTemp("", "raptor-seccomp-*.json")
	if err != nil {
		panic(err)
	}
	_, _ = f.WriteString(wingsdocker.SeccompProfile())
	_ = f.Close()
	return []string{"--security-opt", "no-new-privileges", "--security-opt", "seccomp=" + f.Name()}
}

// Reboot and grow: run in steps around a real VM reboot (see
// scripts/e2e-quotas.sh). The volume lives under /var/lib/raptor-gate.
var gateSpec = ImageSpec{
	Image: "/var/lib/raptor-gate/volumes.xfs", Mountpoint: "/var/lib/raptor-gate/volumes",
	Size: 2 << 30, UnitDir: "/etc/systemd/system",
}

func gateStep(t *testing.T, step string) {
	if os.Getenv("RAPTOR_E2E_GATE_STEP") != step {
		t.Skip("RAPTOR_E2E_GATE_STEP != " + step)
	}
}

func TestGateSetupVolume(t *testing.T) {
	gateStep(t, "setup")
	if err := CreateImage(context.Background(), gateSpec); err != nil {
		t.Fatal(err)
	}
	// An ordering cycle only shows at boot, when systemd drops the job:
	// catch it now.
	unit := filepath.Join(gateSpec.UnitDir, UnitName(gateSpec.Mountpoint))
	if out, err := exec.Command("systemd-analyze", "verify", unit).CombinedOutput(); err != nil || strings.Contains(string(out), "cycle") {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, out)
	}
	m, ok, err := FindMount(gateSpec.Mountpoint)
	if err != nil || !ok || !m.HasProjectQuota() || !DirectIO(m) {
		t.Fatalf("mount %+v ok=%v dio=%v err=%v", m, ok, DirectIO(m), err)
	}
	dir := filepath.Join(gateSpec.Mountpoint, "server")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetProject(dir, 7001); err != nil {
		t.Fatal(err)
	}
	if err := SetLimit(gateSpec.Mountpoint, 7001, 100*mib); err != nil {
		t.Fatal(err)
	}
	if _, err := fill(filepath.Join(dir, "data.bin"), 150*mib); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("limit not enforced: %v", err)
	}
	t.Logf("volume %s mounted from %s (%s), direct I/O on, 100 MiB limit enforced", gateSpec.Mountpoint, m.Source, m.FSType)
}

func TestGateAfterReboot(t *testing.T) {
	gateStep(t, "after-reboot")
	m, ok, err := FindMount(gateSpec.Mountpoint)
	if err != nil || !ok || !m.HasProjectQuota() {
		t.Fatalf("after reboot the volume isn't mounted with project quotas: %+v ok=%v err=%v", m, ok, err)
	}
	t.Logf("after reboot: mounted from %s by systemd, direct I/O before Wings enables it: %v", m.Source, DirectIO(m))
	if err := EnableDirectIO(m); err != nil || !DirectIO(m) {
		t.Fatalf("direct I/O: %v", err)
	}
	u, err := GetUsage(gateSpec.Mountpoint, 7001)
	if err != nil || u.LimitBytes != 100*mib || u.Bytes < 99*mib {
		t.Fatalf("quota after reboot: %+v %v", u, err)
	}
	dir := filepath.Join(gateSpec.Mountpoint, "server")
	if _, err := fill(filepath.Join(dir, "more.bin"), 10*mib); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("limit not enforced after reboot: %v", err)
	}
	if p, _ := Project(filepath.Join(dir, "data.bin")); p != 7001 {
		t.Fatalf("project after reboot: %d", p)
	}
	t.Logf("after reboot: %d MiB used of the %d MiB limit, still enforced", u.Bytes/mib, u.LimitBytes/mib)
}

func TestGateGrowOnline(t *testing.T) {
	gateStep(t, "grow")
	var before unix.Statfs_t
	if err := unix.Statfs(gateSpec.Mountpoint, &before); err != nil {
		t.Fatal(err)
	}
	// Keep a file open and writing across the grow, like a running server.
	dir := filepath.Join(gateSpec.Mountpoint, "server")
	_ = SetLimit(gateSpec.Mountpoint, 7001, 0)
	f, err := os.OpenFile(filepath.Join(dir, "live.bin"), os.O_WRONLY|os.O_CREATE, 0o644) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(make([]byte, mib)); err != nil {
		t.Fatal(err)
	}
	if err := Grow(context.Background(), gateSpec, 3<<30); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, mib)); err != nil {
		t.Fatalf("writing during the grow: %v", err)
	}
	var after unix.Statfs_t
	if err := unix.Statfs(gateSpec.Mountpoint, &after); err != nil {
		t.Fatal(err)
	}
	b, a := int64(before.Blocks)*before.Bsize/mib, int64(after.Blocks)*after.Bsize/mib
	if a < b+900 {
		t.Fatalf("grew from %d MiB to %d MiB", b, a)
	}
	t.Logf("grew online from %d MiB to %d MiB with a file open and writing", b, a)
}
