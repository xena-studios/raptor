package storage

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Data disks (docs/WINGS.md#data-disks): whole disks or partitions the
// owner sets up for Raptor from the node's Disks tab. Each is formatted XFS
// and mounted with project quotas by UUID, by a systemd mount unit, at
// <state dir>/disks/<id> for server data, or /srv/raptor-backups-<id> for
// backups. Wings finds them by where they're mounted, so a disk set up by
// hand with `raptor storage add-disk` is the same as one set up from the
// Panel.

// DiskIDPattern is what a disk ID looks like.
var DiskIDPattern = regexp.MustCompile(`^[a-z0-9]{8}$`)

// NewDiskID returns a random disk ID.
func NewDiskID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Disk roles.
const (
	RoleServers = "servers"
	RoleBackups = "backups"
)

// BackupsMountPrefix is where backup disks are mounted (/srv, so they can be
// folder backup destinations).
const BackupsMountPrefix = "/srv/raptor-backups-"

// MinDiskSize is the smallest disk worth setting up.
const MinDiskSize = 1 << 30

// Uses: what a block device is used for, for people.
const (
	UseEmpty     = "empty"      // can be set up
	UseServers   = "servers"    // a Raptor data disk for servers
	UseBackups   = "backups"    // a Raptor data disk for backups
	UseMain      = "main"       // the main server data volume
	UseSystem    = "system"     // holds the OS, Docker, or Raptor's state
	UseMounted   = "mounted"    // mounted elsewhere by the owner
	UseFormatted = "formatted"  // has a filesystem or other signature, not mounted
	UseParts     = "partitions" // has partitions (each listed)
	UseMember    = "member"     // part of RAID, LVM, or encryption
	UseTooSmall  = "too_small"
	UseReadOnly  = "read_only"
)

// BlockDevice is a disk or partition as Wings reports it.
type BlockDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Type        string        `json:"type"` // disk, part
	Size        int64         `json:"size"`
	Model       string        `json:"model,omitempty"`
	Serial      string        `json:"serial,omitempty"`
	FSType      string        `json:"fstype,omitempty"`
	PTType      string        `json:"pttype,omitempty"`
	Label       string        `json:"label,omitempty"`
	UUID        string        `json:"uuid,omitempty"`
	Mountpoints []string      `json:"mountpoints,omitempty"`
	ReadOnly    bool          `json:"read_only"`
	Removable   bool          `json:"removable"`
	Rotational  bool          `json:"rotational"`
	Children    []BlockDevice `json:"children,omitempty"`
	// Use is what it's used for; DiskID is its Raptor disk ID, for a data
	// disk.
	Use    string `json:"use"`
	DiskID string `json:"disk_id,omitempty"`
}

// lsblkColumns are what Wings asks lsblk for (util-linux 2.37+ for
// MOUNTPOINTS).
const lsblkColumns = "NAME,PATH,MAJ:MIN,TYPE,SIZE,MODEL,SERIAL,FSTYPE,PTTYPE,LABEL,UUID,MOUNTPOINTS,RO,RM,ROTA"

type lsblkDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	MajMin      string        `json:"maj:min"`
	Type        string        `json:"type"`
	Size        int64         `json:"size"`
	Model       *string       `json:"model"`
	Serial      *string       `json:"serial"`
	FSType      *string       `json:"fstype"`
	PTType      *string       `json:"pttype"`
	Label       *string       `json:"label"`
	UUID        *string       `json:"uuid"`
	Mountpoints []*string     `json:"mountpoints"`
	RO          bool          `json:"ro"`
	RM          bool          `json:"rm"`
	Rota        bool          `json:"rota"`
	Children    []lsblkDevice `json:"children"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// Layout is what the classification needs to know about the machine.
type Layout struct {
	// DisksDir is where server data disks are mounted; MainVolume is the
	// main server data volume.
	DisksDir, MainVolume string
	// System are paths whose disks are never offered: /, Raptor's state,
	// Docker's directory.
	System []string
	// Mounts, if set, are where each device (by major:minor) is mounted in
	// the host's mount namespace. Wings' sandbox has its own, full of bind
	// mounts, which lsblk would report instead.
	Mounts map[string][]string
}

// LayoutFor is the layout from Wings' paths: data disks go in the state
// directory's disks/, and the disks holding Raptor's state, logs, local
// backups, Docker, or / are the system's.
func LayoutFor(stateDir, volumes, logs, backups string) Layout {
	return Layout{
		DisksDir: filepath.Join(stateDir, "disks"), MainVolume: volumes,
		System: []string{"/", stateDir, logs, backups, "/var/lib/docker"},
	}
}

// ParseLsblk reads `lsblk -J -b -o <lsblkColumns>` and says what each disk
// is used for. Loop devices (the main volume's image among them), RAM
// disks, and optical drives are left out.
func ParseLsblk(out []byte, l Layout) ([]BlockDevice, error) {
	var doc struct {
		Devices []lsblkDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var disks []BlockDevice
	for _, d := range doc.Devices {
		if d.Type != "disk" || strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "sr") {
			continue
		}
		disks = append(disks, convert(d, l))
	}
	return disks, nil
}

func convert(d lsblkDevice, l Layout) BlockDevice {
	b := BlockDevice{
		Name: d.Name, Path: d.Path, Type: d.Type, Size: d.Size, Model: str(d.Model), Serial: str(d.Serial),
		FSType: str(d.FSType), PTType: str(d.PTType), Label: str(d.Label), UUID: str(d.UUID),
		ReadOnly: d.RO, Removable: d.RM, Rotational: d.Rota,
	}
	if l.Mounts != nil {
		b.Mountpoints = l.Mounts[d.MajMin]
	} else {
		for _, m := range d.Mountpoints {
			if s := str(m); s != "" && !slices.Contains(b.Mountpoints, s) {
				b.Mountpoints = append(b.Mountpoints, s)
			}
		}
	}
	for _, c := range d.Children {
		if c.Type == "part" || c.Type == "lvm" || c.Type == "crypt" || strings.HasPrefix(c.Type, "raid") {
			b.Children = append(b.Children, convert(c, l))
		}
	}
	b.Use, b.DiskID = classify(b, l)
	return b
}

func under(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// allMounts is where a device and everything on it is mounted.
func allMounts(b BlockDevice) []string {
	out := slices.Clone(b.Mountpoints)
	for _, c := range b.Children {
		out = append(out, allMounts(c)...)
	}
	return out
}

func classify(b BlockDevice, l Layout) (use, diskID string) {
	for _, m := range allMounts(b) {
		if m == "[SWAP]" || slices.ContainsFunc(l.System, func(s string) bool { return under(s, m) }) {
			return UseSystem, ""
		}
	}
	for _, m := range b.Mountpoints {
		switch {
		case l.DisksDir != "" && filepath.Dir(m) == filepath.Clean(l.DisksDir):
			return UseServers, filepath.Base(m)
		case strings.HasPrefix(m, BackupsMountPrefix):
			return UseBackups, strings.TrimPrefix(m, BackupsMountPrefix)
		case l.MainVolume != "" && m == l.MainVolume:
			return UseMain, ""
		}
	}
	switch {
	case len(b.Children) > 0:
		if b.Type == "disk" && slices.ContainsFunc(b.Children, func(c BlockDevice) bool { return c.Type != "part" }) {
			return UseMember, ""
		}
		return UseParts, ""
	case len(b.Mountpoints) > 0:
		return UseMounted, ""
	case b.FSType == "linux_raid_member" || b.FSType == "LVM2_member" || b.FSType == "crypto_LUKS" || b.FSType == "zfs_member":
		return UseMember, ""
	case b.FSType != "" || b.PTType != "":
		return UseFormatted, ""
	case b.ReadOnly:
		return UseReadOnly, ""
	case b.Size < MinDiskSize:
		return UseTooSmall, ""
	}
	return UseEmpty, ""
}

// ListDisks runs lsblk and classifies what it finds.
func ListDisks(ctx context.Context, l Layout) ([]BlockDevice, error) {
	if l.Mounts == nil {
		l.Mounts = hostMounts()
	}
	out, err := exec.CommandContext(ctx, "lsblk", "-J", "-b", "-o", lsblkColumns).Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	return ParseLsblk(out, l)
}

// Find returns the device at path among disks and their partitions.
func Find(disks []BlockDevice, path string) (BlockDevice, bool) {
	for _, d := range disks {
		if d.Path == path {
			return d, true
		}
		if c, ok := Find(d.Children, path); ok {
			return c, true
		}
	}
	return BlockDevice{}, false
}
