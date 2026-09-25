package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReserve(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct{ total, override, want int64 }{
		{2 * gib, 0, 1 * gib},        // floor
		{16 * gib, 0, 16 * gib / 10}, // 10%
		{128 * gib, 0, 4 * gib},      // ceiling
		{16 * gib, 3 * gib, 3 * gib}, // override
	} {
		if got := Reserve(c.total, c.override); got != c.want {
			t.Errorf("Reserve(%d, %d) = %d, want %d", c.total, c.override, got, c.want)
		}
	}
}

func TestMemTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("MemTotal:        4005144 kB\nMemFree:          100 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := MemTotal(path)
	if err != nil || got != 4005144*1024 {
		t.Fatalf("got %d, %v", got, err)
	}
}
