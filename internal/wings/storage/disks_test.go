package storage

import (
	"os"
	"testing"
)

func TestParseLsblk(t *testing.T) {
	raw, err := os.ReadFile("testdata/lsblk-vm.json")
	if err != nil {
		t.Fatal(err)
	}
	disks, err := ParseLsblk(raw, Layout{
		DisksDir: "/var/lib/raptor/disks", MainVolume: "/var/lib/raptor/volumes",
		System: []string{"/", "/var/lib/raptor", "/var/lib/docker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/dev/vda": UseSystem, "/dev/vda1": UseSystem, "/dev/vda15": UseMounted, "/dev/vdb": UseEmpty,
		"/dev/vdc": UseMounted, "/dev/sdb": UseServers, "/dev/sdc": UseBackups, "/dev/sdd": UseMember,
		"/dev/sde": UseFormatted, "/dev/sdf": UseTooSmall, "/dev/sdg": UseParts, "/dev/sdg1": UseEmpty,
		"/dev/sdg2": UseMounted,
	}
	for path, use := range want {
		d, ok := Find(disks, path)
		if !ok {
			t.Errorf("%s missing", path)
			continue
		}
		if d.Use != use {
			t.Errorf("%s: %s, want %s", path, d.Use, use)
		}
	}
	if d, _ := Find(disks, "/dev/sdb"); d.DiskID != "abcd1234" {
		t.Errorf("data disk ID: %q", d.DiskID)
	}
	if d, _ := Find(disks, "/dev/sdc"); d.DiskID != "efgh5678" {
		t.Errorf("backups disk ID: %q", d.DiskID)
	}
	for _, skipped := range []string{"/dev/loop0", "/dev/sr0"} {
		if _, ok := Find(disks, skipped); ok {
			t.Errorf("%s listed", skipped)
		}
	}
}
