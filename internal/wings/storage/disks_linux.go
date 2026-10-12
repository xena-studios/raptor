package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DiskSpec is a disk to set up for Raptor.
type DiskSpec struct {
	Device     string // /dev/sdb
	ID         string // its Raptor disk ID
	Role       string // RoleServers or RoleBackups
	Mountpoint string
	UnitDir    string // /etc/systemd/system
	Layout     Layout
}

// MountpointFor is where a disk of a role is mounted.
func MountpointFor(l Layout, role, id string) string {
	if role == RoleBackups {
		return BackupsMountPrefix + id
	}
	return filepath.Join(l.DisksDir, id)
}

// ErrNotEmpty means the device holds something: Raptor never formats a disk
// or partition that has data, partitions, or a signature on it.
var ErrNotEmpty = errors.New("the disk isn't empty")

// SetupDisk formats an empty disk or partition XFS, and mounts it by UUID
// with project quotas through a systemd mount unit, at boot too. It checks
// the device is still empty right before formatting, and mkfs.xfs (without
// -f) refuses anything with a signature on its own.
func SetupDisk(ctx context.Context, s DiskSpec) (uuid string, err error) {
	disks, err := ListDisks(ctx, s.Layout)
	if err != nil {
		return "", err
	}
	d, ok := Find(disks, s.Device)
	if !ok {
		return "", fmt.Errorf("%s isn't a disk on this machine", s.Device)
	}
	if d.Use != UseEmpty {
		return "", fmt.Errorf("%w: %s is %s", ErrNotEmpty, s.Device, d.Use)
	}
	if _, err := os.Stat(s.Mountpoint); err == nil {
		if entries, _ := os.ReadDir(s.Mountpoint); len(entries) > 0 {
			return "", fmt.Errorf("%s already exists and isn't empty", s.Mountpoint)
		}
	}
	label := "raptor-" + s.ID[:min(5, len(s.ID))]                                                                   // XFS labels are at most 12 characters
	if out, err := exec.CommandContext(ctx, "mkfs.xfs", "-q", "-L", label, s.Device).CombinedOutput(); err != nil { //nolint:gosec // a device lsblk listed as empty
		return "", fmt.Errorf("mkfs.xfs: %w: %s", err, bytes.TrimSpace(out))
	}
	// udev names the new filesystem's UUID once it has probed it.
	_ = exec.CommandContext(ctx, "udevadm", "settle", "--timeout=10").Run()
	out, err := exec.CommandContext(ctx, "lsblk", "-n", "-d", "-o", "UUID", s.Device).Output() //nolint:gosec // as above
	uuid = strings.TrimSpace(string(out))
	if err != nil || uuid == "" {
		return "", errors.Join(fmt.Errorf("the new filesystem on %s has no UUID", s.Device), err)
	}
	if err := os.MkdirAll(s.Mountpoint, 0o700); err != nil {
		return uuid, err
	}
	unit := UnitName(s.Mountpoint)
	if err := os.WriteFile(filepath.Join(s.UnitDir, unit), []byte(diskUnit(uuid, s.Mountpoint)), 0o644); err != nil { //nolint:gosec // systemd unit, world-readable by design
		return uuid, err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", unit}} {
		if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil { //nolint:gosec // fixed arguments
			return uuid, fmt.Errorf("systemctl %s: %w: %s", args[0], err, bytes.TrimSpace(out))
		}
	}
	m, ok, err := FindMount(s.Mountpoint)
	if err != nil || !ok || !m.HasProjectQuota() {
		return uuid, errors.Join(fmt.Errorf("%s didn't mount with project quotas", s.Device), err)
	}
	return uuid, nil
}

// diskUnit mounts a data disk, ordered like the main volume's unit (see
// mountUnit): a disk that's gone doesn't stop the box booting, and Wings
// refuses to start servers on it until it's back.
func diskUnit(uuid, mountpoint string) string {
	return fmt.Sprintf(`# Raptor data disk (docs/WINGS.md#data-disks). Written by Wings.
[Unit]
Description=Raptor data disk %s
DefaultDependencies=no
After=local-fs.target
Before=docker.service raptor-wings.service umount.target
Conflicts=umount.target

[Mount]
What=/dev/disk/by-uuid/%s
Where=%s
Type=xfs
Options=prjquota,noatime,nodev,nosuid

[Install]
WantedBy=multi-user.target
`, filepath.Base(mountpoint), uuid, mountpoint)
}
