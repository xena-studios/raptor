package nodelink

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Support bundles (raptor doctor -upload): a linked node signs its upload
// with its node key, so the Panel can attach it to the node and give it
// more room than an anonymous one. The signature covers the bundle's hash,
// the node, and the time, so it can't be moved to another bundle or reused
// after BundleSkew.

// Headers on a signed upload.
const (
	BundleNodeHeader      = "X-Raptor-Node"
	BundleTimeHeader      = "X-Raptor-Time"
	BundleSignatureHeader = "X-Raptor-Signature"
)

// BundlePath is where the Panel takes bundles.
const BundlePath = "/support/bundles"

// AddressPath answers with the address a request came from, as the Panel
// sees it: what the node's hostname should point at (raptor doctor).
const AddressPath = "/nodes/address"

// BundleSkew is how far a signed upload's time may be from the Panel's.
const BundleSkew = 5 * time.Minute

// BundleMessage is what a node signs for an upload.
func BundleMessage(nodeID string, unix int64, body []byte) []byte {
	sum := sha256.Sum256(body)
	return fmt.Appendf(nil, "raptor support bundle v1\n%s\n%d\n%s", nodeID, unix, hex.EncodeToString(sum[:]))
}

// SignBundle returns the headers for a signed upload.
func SignBundle(key ed25519.PrivateKey, nodeID string, now time.Time, body []byte) map[string]string {
	sig := ed25519.Sign(key, BundleMessage(nodeID, now.Unix(), body))
	return map[string]string{
		BundleNodeHeader:      nodeID,
		BundleTimeHeader:      strconv.FormatInt(now.Unix(), 10),
		BundleSignatureHeader: hex.EncodeToString(sig),
	}
}

// VerifyBundle checks a signed upload's time and signature.
func VerifyBundle(pub ed25519.PublicKey, nodeID, unix, sig string, now time.Time, body []byte) error {
	t, err := strconv.ParseInt(unix, 10, 64)
	if err != nil {
		return errors.New("bad time")
	}
	if d := now.Sub(time.Unix(t, 0)); d > BundleSkew || d < -BundleSkew {
		return fmt.Errorf("time is %s off", d.Round(time.Second))
	}
	s, err := hex.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, BundleMessage(nodeID, t, body), s) {
		return errors.New("bad signature")
	}
	return nil
}
