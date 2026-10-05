package link

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The node key (identity.key) proves the node to the Panel on every
// connection (docs/SECURITY-MODEL.md#enrollment-and-identity). It's generated on the
// box and never leaves it: the file holds the base64 Ed25519 seed, readable
// by root only.

// LoadNodeKey reads the node's private key. A missing file means the node
// isn't linked yet: nil, and Wings doesn't connect.
func LoadNodeKey(path string) (ed25519.PrivateKey, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by other users (mode %s): chmod 600 it", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path) //nolint:gosec // path from config
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s: not a base64 Ed25519 seed", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// GenerateNodeKey writes a new node key, refusing to replace one, and
// returns its public key.
func GenerateNodeKey(path string) (ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path from config
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return pub, f.Close()
}
