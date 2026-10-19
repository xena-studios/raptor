package databases

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// An instance with external access serves TLS (docs/DATABASES.md#external-access)
// with a certificate from a certificate authority of its own, made by Wings:
// clients that should check who they're talking to are given the
// authority's certificate (the app shows it), and the server certificate
// names the node's DNS name and the instance's host name. The authority's
// key stays in the instance's folder, which only root reads; the engine
// sees only conf/tls, read-only.

// Where the files are.
func tlsDir(dir string) string    { return filepath.Join(confDir(dir), "tls") }
func caKeyPath(dir string) string { return filepath.Join(dir, "tls-ca.key") }
func caPath(dir string) string    { return filepath.Join(tlsDir(dir), "ca.crt") }
func certPath(dir string) string  { return filepath.Join(tlsDir(dir), "server.crt") }
func keyPath(dir string) string   { return filepath.Join(tlsDir(dir), "server.key") }

// In the container (conf is mounted at /etc/raptor).
const (
	tlsCA   = "/etc/raptor/tls/ca.crt"
	tlsCert = "/etc/raptor/tls/server.crt"
	tlsKey  = "/etc/raptor/tls/server.key"
)

// Lifetimes. The server certificate is made again when it's within
// renewBefore of expiring, or names something else, at the instance's
// next start.
const (
	caLifetime   = 20 * 365 * 24 * time.Hour
	certLifetime = 2 * 365 * 24 * time.Hour
	renewBefore  = 60 * 24 * time.Hour
)

// ensureTLS makes the instance's authority if it has none, and its server
// certificate if it has none, it's expiring, or it doesn't name names.
// It reports whether it made a new server certificate.
func ensureTLS(dir, instance string, names []string) (bool, error) {
	if err := os.MkdirAll(tlsDir(dir), 0o755); err != nil { //nolint:gosec // the engine reads the certificates
		return false, err
	}
	if err := os.Chmod(tlsDir(dir), 0o755); err != nil { //nolint:gosec // whatever Wings' umask
		return false, err
	}
	ca, caKey, err := loadCA(dir)
	if errors.Is(err, fs.ErrNotExist) {
		ca, caKey, err = makeCA(dir, instance)
	}
	if err != nil {
		return false, fmt.Errorf("the instance's certificate authority: %w", err)
	}
	if current(certPath(dir), ca, names) {
		return false, nil
	}
	return true, issue(dir, ca, caKey, names)
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	kb, err := os.ReadFile(caKeyPath(dir))
	if err != nil {
		return nil, nil, err
	}
	cb, err := os.ReadFile(caPath(dir))
	if err != nil {
		return nil, nil, err
	}
	kp, _ := pem.Decode(kb)
	cp, _ := pem.Decode(cb)
	if kp == nil || cp == nil {
		return nil, nil, errors.New("not PEM")
	}
	key, err := x509.ParseECPrivateKey(kp.Bytes)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(cp.Bytes)
	return cert, key, err
}

func makeCA(dir, instance string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Raptor database instance " + instance, Organization: []string{"Raptor"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(caKeyPath(dir), "EC PRIVATE KEY", kb, 0o600, -1); err != nil {
		return nil, nil, err
	}
	if err := writePEM(caPath(dir), "CERTIFICATE", der, 0o644, -1); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// current reports whether the server certificate at path is from ca,
// names exactly names, and isn't about to expire.
func current(path string, ca *x509.Certificate, names []string) bool {
	b, err := os.ReadFile(path) //nolint:gosec // Wings' own folder
	if err != nil {
		return false
	}
	p, _ := pem.Decode(b)
	if p == nil {
		return false
	}
	cert, err := x509.ParseCertificate(p.Bytes)
	if err != nil || cert.CheckSignatureFrom(ca) != nil || time.Until(cert.NotAfter) < renewBefore {
		return false
	}
	have := slices.Sorted(slices.Values(cert.DNSNames))
	want := slices.Sorted(slices.Values(names))
	return slices.Equal(have, want)
}

func issue(dir string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Raptor"}},
		DNSNames:     names,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// The engine's user owns its key: PostgreSQL refuses one others can
	// read, or that it doesn't own.
	if err := writePEM(keyPath(dir), "EC PRIVATE KEY", kb, 0o600, engineUID); err != nil {
		return err
	}
	return writePEM(certPath(dir), "CERTIFICATE", der, 0o644, -1)
}

// writePEM writes a PEM file through a temporary one, with its mode set
// whatever the umask, and owned by uid if it's 0 or more.
func writePEM(path, typ string, der []byte, mode fs.FileMode, uid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if uid >= 0 {
		if err := os.Chown(tmp, uid, uid); err != nil && !errors.Is(err, fs.ErrPermission) {
			return err
		}
	}
	return os.Rename(tmp, path)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand doesn't fail
	}
	return n
}
