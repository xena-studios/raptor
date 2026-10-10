package backup

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// The Panel gets the key only while the owner lets it; the owner can always
// see it (a signed command, or root on the node).
func TestKeyMode(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	if mode, err := v.m.KeyMode(ctx); err != nil || mode != KeyPanel {
		t.Fatalf("default mode: %q, %v", mode, err)
	}
	k, err := v.m.ExportKey(ctx)
	if err != nil || len(k.Key) < 40 || k.Fingerprint != KeyFingerprint(k.Key) || len(k.Fingerprint) != 12 {
		t.Fatalf("export: %+v, %v", k, err)
	}
	if err := v.m.SetKeyMode(ctx, KeyOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := v.m.ExportKey(ctx); !errors.Is(err, ErrOwnerKey) {
		t.Fatalf("exported in owner mode: %v", err)
	}
	if shown, err := v.m.ShowKey(ctx); err != nil || shown.Key != k.Key {
		t.Fatalf("show: %+v, %v", shown, err)
	}
	if err := v.m.SetKeyMode(ctx, "someone"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad mode: %v", err)
	}
	if !slices.Contains(v.eventTypes(), EventKeyMode) {
		t.Fatal("no event")
	}
}
