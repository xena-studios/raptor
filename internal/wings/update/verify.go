// Package update is Wings' self-update (docs/WINGS.md#updates): signed
// releases are downloaded and verified, then started on trial by the running
// version, which only switches to them once they're healthy.
package update

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"aead.dev/minisign"
)

// ReleaseKey verifies releases. It's release/minisign.pub, embedded so a
// compromised download source can't swap it (a test keeps the two equal).
const ReleaseKey = `untrusted comment: minisign public key 90590C599C828C75
RWR1jIKcWQxZkKYiS4FooIdlf/ypDPzeXVpMD6PuOSjTPPhC0SnobYgS
`

// releaseKey is the key updates are verified with. Only the e2e build
// (e2e.go) changes it.
var releaseKey = ReleaseKey

// DefaultKey returns the key updates are verified with.
func DefaultKey() (minisign.PublicKey, error) { return ParseKey(releaseKey) }

// ParseKey parses a minisign public key.
func ParseKey(text string) (minisign.PublicKey, error) {
	var k minisign.PublicKey
	err := k.UnmarshalText([]byte(text))
	return k, err
}

// Checksums verifies checksums.txt against its minisign signature and returns
// the SHA-256 of each file it lists. The signature's trusted comment is signed
// too and must name the release ("raptor <tag> checksums.txt", written by
// scripts/sign-release.sh), so a validly signed checksums file from one release
// can't be passed off as another's.
func Checksums(key minisign.PublicKey, tag string, checksums, sig []byte) (map[string][32]byte, error) {
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		return nil, fmt.Errorf("checksums signature: %w", err)
	}
	if !minisign.Verify(key, checksums, sig) {
		return nil, errors.New("checksums signature is invalid or not made with the release key")
	}
	if want := "raptor " + tag + " checksums.txt"; s.TrustedComment != want {
		return nil, fmt.Errorf("checksums are signed for %q, not %q", s.TrustedComment, want)
	}
	sums := map[string][32]byte{}
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		hexSum, name, ok := strings.Cut(line, "  ")
		var sum [32]byte
		if !ok || name == "" || len(hexSum) != hex.EncodedLen(len(sum)) {
			return nil, fmt.Errorf("checksums: bad line %q", line)
		}
		if _, err := hex.Decode(sum[:], []byte(hexSum)); err != nil {
			return nil, fmt.Errorf("checksums: bad line %q", line)
		}
		sums[name] = sum
	}
	return sums, sc.Err()
}
