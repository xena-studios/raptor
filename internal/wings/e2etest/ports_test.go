package e2etest

import "testing"

func TestFreePort(t *testing.T) {
	seen := map[int]bool{}
	for range 50 {
		p := FreePort(t)
		if p < portLow || p >= ephemeralStart() {
			t.Fatalf("port %d outside [%d, %d)", p, portLow, ephemeralStart())
		}
		seen[p] = true
	}
	if len(seen) < 40 {
		t.Errorf("only %d different ports in 50 picks", len(seen))
	}
}
