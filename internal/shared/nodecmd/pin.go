package nodecmd

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
)

// OwnerPin is how a node trusts its owner's passkey from the start
// (docs/SECURITY-MODEL.md#passkey-signed-commands): when the owner makes a
// join token, their passkey signs a statement naming the token (by hash)
// and the passkey's public key. The Panel hands it to the node when the
// node links; Wings checks the signature and that the statement names the
// very token it used, then pins the key. A Panel can relay it but not make
// one up.
type OwnerPin struct {
	JoinTokenHash []byte            `json:"join_token_hash"` // SHA-256 of the rpt_join_… token
	CredentialID  []byte            `json:"credential_id"`
	PublicKey     []byte            `json:"public_key"` // COSE_Key
	UserID        string            `json:"user_id"`
	Name          string            `json:"name"`
	Signature     *PasskeySignature `json:"signature"`
}

// PinPurpose keeps an owner pin's signature from passing as anything else.
const PinPurpose = "raptor.owner_pin.v1"

// JoinTokenHash is what an owner pin names.
func JoinTokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Canonical is what the passkey signs the hash of: every field but the
// signature, as RFC 8785 JSON (byte fields in standard base64), with the
// purpose. The browser builds the same bytes (web/src/lib/canonical.ts).
func (p OwnerPin) Canonical() ([]byte, error) {
	raw, err := json.Marshal(struct {
		Purpose       string `json:"purpose"`
		JoinTokenHash []byte `json:"join_token_hash"`
		CredentialID  []byte `json:"credential_id"`
		PublicKey     []byte `json:"public_key"`
		UserID        string `json:"user_id"`
		Name          string `json:"name"`
	}{PinPurpose, p.JoinTokenHash, p.CredentialID, p.PublicKey, p.UserID, p.Name})
	if err != nil {
		return nil, err
	}
	return jcs.Transform(raw)
}

// Hash is the WebAuthn challenge the passkey signed.
func (p OwnerPin) Hash() ([]byte, error) {
	c, err := p.Canonical()
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(c)
	return h[:], nil
}

// Check checks a pin's shape and that it names token. The signature itself
// is checked by Wings, which knows its relying party.
func (p OwnerPin) Check(token string) error {
	switch {
	case p.Signature == nil:
		return errors.New("the owner key isn't signed")
	case len(p.CredentialID) == 0 || len(p.PublicKey) == 0 || p.UserID == "":
		return errors.New("the owner key is incomplete")
	case string(p.Signature.CredentialID) != string(p.CredentialID):
		return errors.New("the owner key wasn't signed by itself")
	case string(p.JoinTokenHash) != string(JoinTokenHash(token)):
		return errors.New("the owner key was signed for a different join token")
	}
	return nil
}
