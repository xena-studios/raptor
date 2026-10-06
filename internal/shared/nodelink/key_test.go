package nodelink

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "raptor", "node.key")
	if k, err := LoadKey(p); k != nil || err != nil {
		t.Fatalf("missing: %v, %v", k, err)
	}
	pub, err := GenerateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	k, err := LoadKey(p)
	if err != nil || !k.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("load: %v", err)
	}
	if _, err := GenerateKey(p); err == nil {
		t.Error("replaced an existing key")
	}
	_ = os.Chmod(p, 0o644)
	if _, err := LoadKey(p); err == nil {
		t.Error("loaded a world-readable key")
	}
}
