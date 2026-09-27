package main

import "testing"

func TestHuman(t *testing.T) {
	for n, want := range map[int64]string{
		0:             "0 B",
		1023:          "1023 B",
		1024:          "1.0 KiB",
		1536:          "1.5 KiB",
		10 << 30:      "10.0 GiB",
		3 << 40:       "3.0 TiB",
		5<<30 + 1<<29: "5.5 GiB",
	} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}
