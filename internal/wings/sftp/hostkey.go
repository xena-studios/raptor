package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadHostKey reads the node's SSH host key, generating an Ed25519 key on
// first use. The key is created on the node and never leaves it; users check
// its fingerprint, which the Panel shows (docs/SECURITY-MODEL.md).
func LoadHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-provided path
	if errors.Is(err, fs.ErrNotExist) {
		return generateHostKey(path)
	}
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("sftp host key %s: %w", path, err)
	}
	return signer, nil
}

func generateHostKey(path string) (ssh.Signer, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(key, "raptor sftp host key")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: two starts racing never overwrite each other's key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-provided path
	if errors.Is(err, fs.ErrExist) {
		return LoadHostKey(path)
	}
	if err != nil {
		return nil, err
	}
	_, err = f.Write(pem.EncodeToMemory(block))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}

// Fingerprint returns a host key's SHA-256 fingerprint as OpenSSH prints it.
func Fingerprint(k ssh.Signer) string { return ssh.FingerprintSHA256(k.PublicKey()) }
