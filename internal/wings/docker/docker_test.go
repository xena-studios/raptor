package docker

import "testing"

// Pterodactyl's overhead: +15% up to 2 GiB, +10% up to 4 GiB, +5% above.
func TestBoundedMemory(t *testing.T) {
	for _, c := range []struct{ mib, want int64 }{
		{1024, 1177},
		{2048, 2355},
		{4096, 4505},
		{8192, 8601},
	} {
		if got := boundedMemory(c.mib) / (1 << 20); got != c.want {
			t.Errorf("boundedMemory(%d) = %d MiB, want %d", c.mib, got, c.want)
		}
	}
}
