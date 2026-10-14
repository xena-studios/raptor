package actions

import (
	"slices"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/doctor"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/system"
)

func TestDataDisksInHealth(t *testing.T) {
	data := []system.DataDisk{
		{ID: system.MainDisk, Role: "servers", Mountpoint: "/var/lib/raptor/volumes", Total: 100, Free: 40, Mounted: true},
		{ID: "abcd1234", Role: "servers", Mountpoint: "/var/lib/raptor/disks/abcd1234", Total: 500, Free: 300, Mounted: true},
		{ID: "efgh5678", Role: "backups", Mountpoint: "/var/lib/raptor/disks/efgh5678", Total: 800, Free: 800, Mounted: true},
		{ID: "gone0000", Role: "servers", Mountpoint: "/var/lib/raptor/disks/gone0000"},
	}
	labels := map[string]string{"/var/lib/raptor/volumes": "Servers' files"}
	spaces := []host.DiskSpace{{Path: "/var/lib/raptor/volumes", Total: 100, Free: 40}}
	spaces, got := withDataDisks(spaces, labels, data)
	if len(labels) != 1 {
		t.Fatal("the labels passed in changed")
	}
	disks := groupDisks(spaces, got)
	if len(disks) != 3 {
		t.Fatalf("disks = %+v, want the main volume and two data disks", disks)
	}
	if l := disks[1].Paths[0].Label; l != "Data disk abcd1234 (servers' files)" {
		t.Errorf("label = %q", l)
	}
	if l := disks[2].Paths[0].Label; l != "Data disk efgh5678 (backups)" {
		t.Errorf("label = %q", l)
	}
	if n := ServerStorage(data); n != 600 {
		t.Errorf("server storage = %d, want the main volume and the servers' data disk", n)
	}
}

// The Panel hears the host disk now, and each check that failed or warned
// once, however many results it gave.
func TestHealthSummary(t *testing.T) {
	h := &Health{Disk: &host.DiskGuard{MinFree: 100, Space: func(string) (int64, int64, error) { return 1000, 10, nil }}}
	h.Disk.Watch("/var/lib/raptor")
	if s := h.Summary(); !s.GetHostDiskLow() || s.GetCheckedAt() != 0 || len(s.GetFailing()) != 0 {
		t.Errorf("before the checks: %v", s)
	}
	h.results = []doctor.Result{
		{ID: "os", Status: doctor.Pass},
		{ID: "data-disks", Status: doctor.Fail},
		{ID: "data-disks", Status: doctor.Fail},
		{ID: "ssh", Status: doctor.Warn},
		{ID: "clock", Status: doctor.Skip},
	}
	h.ranAt = time.UnixMilli(1234)
	s := h.Summary()
	if !slices.Equal(s.GetFailing(), []string{"data-disks"}) || !slices.Equal(s.GetWarning(), []string{"ssh"}) || s.GetCheckedAt() != 1234 {
		t.Errorf("summary: %v", s)
	}
}
