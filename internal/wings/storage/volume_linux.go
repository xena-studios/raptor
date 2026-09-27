package storage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Loop device ioctls (linux/loop.h).
const (
	loopSetCapacity = 0x4C07
	loopSetDirectIO = 0x4C08
)

// Mount is what's mounted at a path.
type Mount struct {
	Source  string // e.g. /dev/loop0
	FSType  string // e.g. xfs
	Options string // superblock options, e.g. "rw,...,prjquota"
}

// FindMount returns the filesystem mounted exactly at path, from
// /proc/self/mountinfo. ok is false if path isn't a mount point, which is the
// case to catch: servers must never write into the empty directory under a
// missing volume.
func FindMount(path string) (m Mount, ok bool, err error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return Mount{}, false, err
	}
	defer func() { _ = f.Close() }()
	path = filepath.Clean(path)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// id parent major:minor root mountpoint options [optional...] - fstype source superopts
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, x := range fields {
			if x == "-" {
				sep = i
				break
			}
		}
		if sep < 5 || len(fields) < sep+4 {
			continue
		}
		if unescapeMountinfo(fields[4]) == path {
			// The last matching line wins: later mounts shadow earlier ones.
			m, ok = Mount{Source: fields[sep+2], FSType: fields[sep+1], Options: fields[sep+3]}, true
		}
	}
	return m, ok, sc.Err()
}

// unescapeMountinfo decodes the octal escapes mountinfo uses for spaces etc.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// HasProjectQuota reports whether an XFS mount enforces project quotas.
func (m Mount) HasProjectQuota() bool {
	if m.FSType != "xfs" {
		return false
	}
	for _, o := range strings.Split(m.Options, ",") {
		if o == "prjquota" || o == "pquota" || o == "pqnoenforce" {
			return o != "pqnoenforce"
		}
	}
	return false
}

// EnableDirectIO turns on direct I/O for the loop device behind a mount, so
// data isn't cached twice (once for the image file on the host filesystem,
// once for the XFS inside it). A mount unit can't set it, so Wings does, and
// it can be changed while mounted. Not a loop device: nothing to do.
func EnableDirectIO(m Mount) error {
	if !strings.HasPrefix(m.Source, "/dev/loop") {
		return nil
	}
	f, err := os.OpenFile(m.Source, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := unix.IoctlSetInt(int(f.Fd()), loopSetDirectIO, 1); err != nil {
		return fmt.Errorf("enable direct I/O on %s: %w", m.Source, err)
	}
	return nil
}

// DirectIO reports whether a loop device has direct I/O on.
func DirectIO(m Mount) bool {
	b, err := os.ReadFile(filepath.Join("/sys/block", filepath.Base(m.Source), "loop/dio"))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// ImageSpec describes the tier 2 volume: an XFS filesystem in a preallocated
// image file, loop-mounted by a systemd mount unit.
type ImageSpec struct {
	Image      string // e.g. /var/lib/raptor/volumes.xfs
	Mountpoint string // e.g. /var/lib/raptor/volumes
	Size       int64  // bytes
	UnitDir    string // /etc/systemd/system
}

// UnitName is the systemd mount unit for a mount point (systemd-escape --path).
func UnitName(mountpoint string) string {
	p := strings.Trim(filepath.Clean(mountpoint), "/")
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/':
			b.WriteByte('-')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.' && i > 0, c == ':':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String() + ".mount"
}

// mountUnit is the systemd mount unit for the volume.
//
//   - DefaultDependencies=no: local mounts are otherwise ordered before
//     local-fs.target, which combined with mounting after local filesystems
//     is an ordering cycle, and systemd silently drops the mount at boot (the
//     gate caught exactly that). Ordering is spelled out instead.
//   - It mounts after local filesystems (including whichever one holds the
//     image, via RequiresMountsFor), before Docker and Wings start, and is
//     unmounted at shutdown only after they stop.
//   - Pulled in by multi-user.target, not local-fs.target: if the volume
//     can't mount, the box still boots normally (a remote box stuck in
//     emergency mode can't be fixed without console access). Wings then
//     refuses to start servers and says why.
func mountUnit(s ImageSpec) string {
	return fmt.Sprintf(`# Raptor server data volume (docs/WINGS.md#disk-quotas). Written by Wings.
[Unit]
Description=Raptor server data volume
DefaultDependencies=no
RequiresMountsFor=%s
After=local-fs.target
Before=docker.service raptor-wings.service umount.target
Conflicts=umount.target

[Mount]
What=%s
Where=%s
Type=xfs
Options=loop,prjquota,noatime,nodev,nosuid

[Install]
WantedBy=multi-user.target
`, filepath.Dir(s.Image), s.Image, s.Mountpoint)
}

// CreateImage creates the tier 2 volume: preallocates the image (so the
// space is reserved and not fragmented), formats it XFS, writes and starts
// the mount unit, and turns on direct I/O. It refuses to touch an existing
// image.
func CreateImage(ctx context.Context, s ImageSpec) error {
	if _, err := os.Stat(s.Image); err == nil {
		return fmt.Errorf("%s already exists", s.Image)
	}
	if err := os.MkdirAll(s.Mountpoint, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = unix.Fallocate(int(f.Fd()), 0, 0, s.Size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(s.Image)
		return fmt.Errorf("preallocate %s: %w", s.Image, err)
	}
	if out, err := exec.CommandContext(ctx, "mkfs.xfs", "-q", "-L", "raptor", s.Image).CombinedOutput(); err != nil { //nolint:gosec // path from the root-owned config
		_ = os.Remove(s.Image)
		return fmt.Errorf("mkfs.xfs: %w: %s", err, bytes.TrimSpace(out))
	}
	unit := UnitName(s.Mountpoint)
	if err := os.WriteFile(filepath.Join(s.UnitDir, unit), []byte(mountUnit(s)), 0o644); err != nil { //nolint:gosec // systemd unit, world-readable by design
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", unit}} {
		if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil { //nolint:gosec // fixed arguments
			return fmt.Errorf("systemctl %s: %w: %s", args[0], err, bytes.TrimSpace(out))
		}
	}
	m, ok, err := FindMount(s.Mountpoint)
	if err != nil || !ok {
		return errors.Join(errors.New("the volume didn't mount"), err)
	}
	return EnableDirectIO(m)
}

// Grow enlarges the tier 2 volume online: extends the image, tells the loop
// device, and grows XFS, all while servers keep running.
func Grow(ctx context.Context, s ImageSpec, newSize int64) error {
	fi, err := os.Stat(s.Image)
	if err != nil {
		return err
	}
	if newSize <= fi.Size() {
		return fmt.Errorf("new size %d isn't larger than the current %d", newSize, fi.Size())
	}
	m, ok, err := FindMount(s.Mountpoint)
	if err != nil || !ok || !strings.HasPrefix(m.Source, "/dev/loop") {
		return errors.Join(errors.New("the volume isn't mounted from a loop device"), err)
	}
	f, err := os.OpenFile(s.Image, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	err = unix.Fallocate(int(f.Fd()), 0, fi.Size(), newSize-fi.Size())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("extend %s: %w", s.Image, err)
	}
	loop, err := os.OpenFile(m.Source, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = unix.IoctlSetInt(int(loop.Fd()), loopSetCapacity, 0)
	_ = loop.Close()
	if err != nil {
		return fmt.Errorf("refresh %s capacity: %w", m.Source, err)
	}
	if out, err := exec.CommandContext(ctx, "xfs_growfs", s.Mountpoint).CombinedOutput(); err != nil { //nolint:gosec // path from the root-owned config
		return fmt.Errorf("xfs_growfs: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// BackingFile returns the image file behind a loop device mount, or "".
func BackingFile(m Mount) string {
	if !strings.HasPrefix(m.Source, "/dev/loop") {
		return ""
	}
	b, err := os.ReadFile(filepath.Join("/sys/block", filepath.Base(m.Source), "loop/backing_file"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Space returns the size and free space of the filesystem holding path.
func Space(path string) (total, free int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Blocks) * st.Bsize, int64(st.Bavail) * st.Bsize, nil //nolint:gosec // block counts fit in int64
}
