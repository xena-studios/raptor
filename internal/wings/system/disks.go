package system

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Data disks (docs/WINGS.md#data-disks): the machine's disks, setting up an
// empty one for Raptor, and which disk new servers go on.

// MainDisk names the main server data volume in the API (it's ” in the
// database).
const MainDisk = "main"

// defaultDiskKey holds the default disk for new servers in kv; no row is
// the main volume.
const defaultDiskKey = "servers.default_disk"

// Disks is what the Disks tab shows.
type Disks struct {
	// Devices are the machine's disks and partitions, with what each is
	// used for.
	Devices []storage.BlockDevice `json:"devices"`
	// Data are where servers' files and backups can go: the main volume,
	// and every data disk, including missing ones servers are on.
	Data []DataDisk `json:"data"`
	// Default is the disk new servers go on.
	Default string `json:"default"`
}

// DataDisk is a place Raptor keeps data.
type DataDisk struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	Mountpoint string `json:"mountpoint"`
	Device     string `json:"device,omitempty"`
	Model      string `json:"model,omitempty"`
	Total      int64  `json:"total"`
	Free       int64  `json:"free"`
	Servers    int64  `json:"servers"`
	// Mounted is false for a disk servers are on that isn't there.
	Mounted bool `json:"mounted"`
	// Image: the main volume is an image file on the system disk.
	Image bool `json:"image,omitempty"`
}

// DiskManager lists and sets up disks.
type DiskManager struct {
	DB     *store.DB
	Layout storage.Layout
	// List lists the machine's disks (storage.ListDisks).
	List func(ctx context.Context, l storage.Layout) ([]storage.BlockDevice, error)
	// Space returns a path's filesystem size and free bytes.
	Space func(path string) (total, free int64, err error)
	// Mounted reports whether a path is a mount point with project quotas,
	// and whether it's an image file.
	Mounted func(path string) (ok, image bool)
	// AddDisk sets up a disk outside Wings' sandbox (`raptor storage
	// add-disk` through systemd-run) and returns its ID.
	AddDisk func(ctx context.Context, device, role string) (string, error)
	Log     *slog.Logger
}

// Disks lists the machine's disks and Raptor's data disks.
func (d *DiskManager) Disks(ctx context.Context) (Disks, error) {
	devs, err := d.List(ctx, d.Layout)
	if err != nil {
		return Disks{}, err
	}
	counts := map[string]int64{}
	rows, err := d.DB.Read.CountServersOnDisks(ctx)
	if err != nil {
		return Disks{}, err
	}
	for _, r := range rows {
		counts[r.DiskID] = r.Servers
	}
	out := Disks{Devices: devs}
	main := DataDisk{ID: MainDisk, Role: storage.RoleServers, Mountpoint: d.Layout.MainVolume, Servers: counts[""]}
	main.Mounted, main.Image = d.Mounted(main.Mountpoint)
	main.Total, main.Free, _ = d.Space(main.Mountpoint)
	out.Data = append(out.Data, main)
	var walk func([]storage.BlockDevice)
	walk = func(list []storage.BlockDevice) {
		for _, b := range list {
			if b.DiskID != "" {
				dd := DataDisk{
					ID: b.DiskID, Role: storage.RoleServers, Device: b.Path, Model: b.Model, Mounted: true,
					Mountpoint: storage.MountpointFor(d.Layout, b.Use, b.DiskID), Servers: counts[b.DiskID],
				}
				if b.Use == storage.UseBackups {
					dd.Role = storage.RoleBackups
				}
				dd.Total, dd.Free, _ = d.Space(dd.Mountpoint)
				out.Data = append(out.Data, dd)
			}
			walk(b.Children)
		}
	}
	walk(devs)
	// A disk servers are on that's gone: it shows, so its servers' state
	// makes sense.
	for id, n := range counts {
		if id != "" && !slices.ContainsFunc(out.Data, func(x DataDisk) bool { return x.ID == id }) {
			out.Data = append(out.Data, DataDisk{
				ID: id, Role: storage.RoleServers, Servers: n, Mountpoint: storage.MountpointFor(d.Layout, storage.RoleServers, id),
			})
		}
	}
	slices.SortStableFunc(out.Data[1:], func(a, b DataDisk) int { return strings.Compare(a.ID, b.ID) })
	out.Default, err = d.Default(ctx)
	return out, err
}

// Default is the disk new servers go on: MainDisk or a data disk's ID.
func (d *DiskManager) Default(ctx context.Context) (string, error) {
	v, err := d.DB.Read.GetKV(ctx, defaultDiskKey)
	if errors.Is(err, sql.ErrNoRows) {
		return MainDisk, nil
	}
	if err != nil {
		return "", err
	}
	var id string
	if err := json.Unmarshal(v, &id); err != nil || id == "" {
		return MainDisk, nil //nolint:nilerr // an unreadable value is the main volume
	}
	return id, nil
}

// serverDisk finds a data disk for servers that's mounted now.
func (d *DiskManager) serverDisk(ctx context.Context, id string) error {
	if id == MainDisk {
		return nil
	}
	disks, err := d.Disks(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(disks.Data, func(x DataDisk) bool {
		return x.ID == id && x.Role == storage.RoleServers && x.Mounted
	}) {
		return invalidf("there's no disk %q for server data on this node", id)
	}
	return nil
}

// SetDefault picks the disk new servers go on.
func (d *DiskManager) SetDefault(ctx context.Context, id string) error {
	if err := d.serverDisk(ctx, id); err != nil {
		return err
	}
	b, _ := json.Marshal(id)
	return d.DB.Write.SetKV(ctx, store.SetKVParams{Key: defaultDiskKey, Value: b})
}

// ForServer is which disk a new server goes on, in the database's terms
// (” is the main volume): the one asked for, or the default.
func (d *DiskManager) ForServer(ctx context.Context, asked string) (string, error) {
	if asked == "" {
		def, err := d.Default(ctx)
		if err != nil {
			return "", err
		}
		asked = def
	}
	if err := d.serverDisk(ctx, asked); err != nil {
		return "", err
	}
	if asked == MainDisk {
		return "", nil
	}
	return asked, nil
}

// Setup formats an empty disk for Raptor and mounts it. Nothing with data,
// partitions, or a filesystem on it is touched: Wings checks here, and
// again right before formatting.
func (d *DiskManager) Setup(ctx context.Context, device, role string, size int64, serial string) (string, error) {
	if role != storage.RoleServers && role != storage.RoleBackups {
		return "", invalidf(`a disk is for "servers" or "backups"`)
	}
	devs, err := d.List(ctx, d.Layout)
	if err != nil {
		return "", err
	}
	b, ok := storage.Find(devs, device)
	if !ok {
		return "", invalidf("%s isn't a disk on this machine", device)
	}
	if b.Size != size || b.Serial != serial {
		return "", invalidf("%s isn't the disk you picked any more (device names can change when the machine restarts). Reload the page and pick it again", device)
	}
	if b.Use != storage.UseEmpty {
		return "", invalidf("%s isn't empty, so Raptor won't format it. Only disks with no partitions, filesystem, or data are set up from here", device)
	}
	id, err := d.AddDisk(ctx, device, role)
	if err != nil {
		if d.Log != nil {
			d.Log.Error("setting up a disk failed", "device", device, "role", role, "err", err)
		}
		return "", fmt.Errorf("setting up %s failed. The node's log has the details: journalctl -u raptor-wings", device)
	}
	if d.Log != nil {
		d.Log.Info("disk set up", "device", device, "role", role, "disk", id)
	}
	return id, nil
}
