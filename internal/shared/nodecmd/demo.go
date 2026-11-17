package nodecmd

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// The demo key (#449, docs/SECURITY-MODEL.md#the-demo-key). A guest in
// the public demo has no passkey, so the Panel signs the few passkey-signed
// commands the demo needs (making and deleting a guest's sandbox, and
// restoring a showcase server's locked known-good backup) with a key of
// its own. Only a node whose owner ran `raptor demo on` with that key's
// public half trusts it, and even then only for those commands. Every other
// node refuses it, so the Panel holding it changes nothing for them.

// demoKeyInfo separates the demo key from every other use of the Panel's
// key. Each node has its own: a demo key never verifies on another node.
const demoKeyInfo = "raptor demo key v1:"

// DemoKey is a node's demo key, derived from the Panel's signing key and
// the node's ID (HKDF-SHA256). The Panel derives it when it needs it, so
// there's nothing new to store or lose, and `panel staff demo node` shows
// its public half for `raptor demo on`.
func DemoKey(panelKey ed25519.PrivateKey, nodeID string) (ed25519.PrivateKey, error) {
	if len(panelKey) != ed25519.PrivateKeySize || nodeID == "" {
		return nil, errors.New("demo key: need the Panel's key and a node ID")
	}
	seed, err := hkdf.Key(sha256.New, panelKey.Seed(), nil, demoKeyInfo+nodeID, ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// demoContext starts what a demo signature covers, so it never verifies as
// any other signature by the same key.
const demoContext = "raptor demo command v1\n"

// DemoPayload is what the demo key signs for a command: the command's hash
// (Envelope.Hash, the same bytes a passkey signs), so it covers the action,
// the server, the params, the node, the user, and the expiry.
func DemoPayload(hash []byte) []byte {
	return []byte(demoContext + hex.EncodeToString(hash))
}

// SignDemo adds the demo key's signature to a command whose fields are set.
func SignDemo(demoKey ed25519.PrivateKey, e *Envelope) error {
	h, err := e.Hash()
	if err != nil {
		return err
	}
	e.Demo = ed25519.Sign(demoKey, DemoPayload(h))
	return nil
}
