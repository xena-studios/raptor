package server

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	// UUIDv7s created in the same minute share their first characters.
	a := "0199a000-0000-7000-8000-00000000aaaa"
	b := "0199a000-0000-7000-8000-00000000bbbb"
	m := &Manager{servers: map[string]*instance{a: nil, b: nil}}

	for ref, want := range map[string]string{a: a, "0000aaaa": a, "0000BBBB": b} {
		if got, err := m.Resolve(ref); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	for _, ref := range []string{"0199a000", "000aaaa", "00000000aaaa", "", "ffffaaaa"} {
		if _, err := m.Resolve(ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q): %v, want not found", ref, err)
		}
	}
	// A short ID two servers share is refused, not guessed.
	c := "0199a001-0000-7000-8000-00000000aaaa"
	m.servers[c] = nil
	if _, err := m.Resolve("0000aaaa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ambiguous: %v", err)
	}
}
