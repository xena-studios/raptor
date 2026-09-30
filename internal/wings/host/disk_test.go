package host

import (
	"errors"
	"strings"
	"testing"
)

func TestDiskGuard(t *testing.T) {
	free := map[string]int64{"/var/lib/docker": 50 << 30, "/var/lib/raptor": 40 << 30}
	var changes []bool
	g := &DiskGuard{
		MinFree: 10 << 30,
		Space: func(p string) (int64, int64, error) {
			f, ok := free[p]
			if !ok {
				return 0, 0, errors.New("no such file")
			}
			return 100 << 30, f, nil
		},
		OnChange: func(low bool, _ []DiskSpace) { changes = append(changes, low) },
	}
	g.Watch("/var/lib/docker", "/var/lib/raptor", "/missing", "/var/lib/docker")
	if err := g.Err(); err != nil {
		t.Fatalf("plenty of space: %v", err)
	}
	if _, spaces := g.Check(); len(spaces) != 2 {
		t.Errorf("spaces = %+v (missing paths are skipped)", spaces)
	}
	free["/var/lib/raptor"] = 3 << 30
	err := g.Err()
	if !errors.Is(err, ErrLowDisk) || !strings.Contains(err.Error(), "/var/lib/raptor has 3.0 GiB free") {
		t.Fatalf("low: %v", err)
	}
	g.Check() // still low: no second change
	free["/var/lib/raptor"] = 30 << 30
	if err := g.Err(); err != nil {
		t.Fatalf("recovered: %v", err)
	}
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Errorf("changes = %v, want [true false]", changes)
	}
	var off *DiskGuard
	if err := off.Err(); err != nil {
		t.Errorf("nil guard: %v", err)
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 10 << 30: "10.0 GiB", 3 << 40: "3.0 TiB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
