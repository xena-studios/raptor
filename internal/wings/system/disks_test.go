package system

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

func TestDisks(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// A server on a data disk that's gone.
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: "s1", Name: "s", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", DiskID: "gone0000",
	}); err != nil {
		t.Fatal(err)
	}
	devs := []storage.BlockDevice{
		{Path: "/dev/sda", Size: 100, Use: storage.UseSystem},
		{Path: "/dev/sdb", Size: 500 << 30, Serial: "S1", Use: storage.UseEmpty},
		{Path: "/dev/sdc", Size: 500 << 30, Use: storage.UseServers, DiskID: "abcd1234", Model: "Fast"},
		{Path: "/dev/sdd", Size: 500 << 30, Use: storage.UseBackups, DiskID: "efgh5678"},
	}
	var added []string
	d := &DiskManager{
		DB:     db,
		Layout: storage.Layout{DisksDir: "/state/disks", MainVolume: "/v"},
		List:   func(context.Context, storage.Layout) ([]storage.BlockDevice, error) { return devs, nil },
		Space:  func(string) (int64, int64, error) { return 100, 40, nil },
		Mounted: func(p string) (bool, bool) {
			return true, p == "/v"
		},
		AddDisk: func(_ context.Context, device, role string) (string, error) {
			added = append(added, device+" "+role)
			return "newdisk1", nil
		},
	}
	got, err := d.Disks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]DataDisk{}
	for _, x := range got.Data {
		ids[x.ID] = x
	}
	if len(got.Data) != 4 || !ids["main"].Image || ids["abcd1234"].Mountpoint != "/state/disks/abcd1234" ||
		ids["efgh5678"].Role != storage.RoleBackups || ids["efgh5678"].Mountpoint != "/srv/raptor-backups-efgh5678" ||
		ids["gone0000"].Mounted || ids["gone0000"].Servers != 1 || got.Default != MainDisk {
		t.Fatalf("disks: %+v", got)
	}

	// The default: only a mounted disk for servers.
	for _, bad := range []string{"efgh5678", "gone0000", "nope"} {
		if err := d.SetDefault(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("default %s: %v", bad, err)
		}
	}
	if disk, err := d.ForServer(ctx, ""); err != nil || disk != "" {
		t.Errorf("main by default: %q, %v", disk, err)
	}
	if err := d.SetDefault(ctx, "abcd1234"); err != nil {
		t.Fatal(err)
	}
	if disk, _ := d.ForServer(ctx, ""); disk != "abcd1234" {
		t.Errorf("default disk: %q", disk)
	}
	if disk, _ := d.ForServer(ctx, MainDisk); disk != "" {
		t.Errorf("asked for main: %q", disk)
	}
	if _, err := d.ForServer(ctx, "gone0000"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a missing disk: %v", err)
	}

	// Setting up: only an empty disk, the one the owner saw.
	for _, c := range []struct {
		device, role, serial string
		size                 int64
	}{
		{"/dev/sda", "servers", "", 100},            // system
		{"/dev/sdc", "servers", "", 500 << 30},      // in use
		{"/dev/sdz", "servers", "", 1},              // not there
		{"/dev/sdb", "servers", "S2", 500 << 30},    // another disk now has that name
		{"/dev/sdb", "servers", "S1", 400 << 30},    // likewise
		{"/dev/sdb", "everything", "S1", 500 << 30}, // no such role
	} {
		if _, err := d.Setup(ctx, c.device, c.role, c.size, c.serial); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if len(added) != 0 {
		t.Fatalf("formatted: %v", added)
	}
	if id, err := d.Setup(ctx, "/dev/sdb", "backups", 500<<30, "S1"); err != nil || id != "newdisk1" || added[0] != "/dev/sdb backups" {
		t.Errorf("setup: %q, %v, %v", id, err, added)
	}
	d.AddDisk = func(context.Context, string, string) (string, error) {
		return "", errors.New("mkfs.xfs: exit status 1: junk")
	}
	if _, err := d.Setup(ctx, "/dev/sdb", "servers", 500<<30, "S1"); err == nil || errors.Is(err, ErrInvalid) || len(err.Error()) == 0 || strings.Contains(err.Error(), "mkfs") {
		t.Errorf("a failure reads as a sentence: %v", err)
	}

	// Growing: a mounted data disk only; the main volume is grown on the node.
	var grown []string
	d.GrowDisk = func(_ context.Context, id string) error {
		grown = append(grown, id)
		return nil
	}
	for _, bad := range []string{MainDisk, "gone0000", "nope"} {
		if _, err := d.Grow(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("grow %s: %v", bad, err)
		}
	}
	if x, err := d.Grow(ctx, "abcd1234"); err != nil || x.ID != "abcd1234" || x.Size != 500<<30 || len(grown) != 1 {
		t.Errorf("grow: %+v, %v, %v", x, err, grown)
	}
	if n := len(d.Data(ctx)); n != 4 {
		t.Errorf("data disks: %d", n)
	}
}
