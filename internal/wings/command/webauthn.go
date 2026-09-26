package command

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// WebAuthn authenticator data flags.
const (
	flagUserPresent  = 0x01
	flagUserVerified = 0x04
	minAuthDataLen   = 37 // rpIdHash (32) + flags (1) + signCount (4)
)

// Relying party identity: signatures are only valid when made on this
// origin, for this RP ID.
type RelyingParty struct {
	Origin string // "https://raptorpanel.net"
	ID     string // "raptorpanel.net"
}

var errBadAssertion = errors.New("invalid passkey signature")

// verifyAssertion checks a WebAuthn assertion (W3C WebAuthn §7.2, the steps
// that apply when the relying party didn't issue the challenge itself: here
// the challenge is the command's hash, and replay protection comes from the
// command ID).
//
// It returns the authenticator's signature counter.
func verifyAssertion(coseKey []byte, s PasskeySignature, challenge []byte, rp RelyingParty) (uint32, error) {
	fail := func(format string, a ...any) (uint32, error) {
		return 0, fmt.Errorf("%w: %s", errBadAssertion, fmt.Sprintf(format, a...))
	}
	ad := s.AuthenticatorData
	if len(ad) < minAuthDataLen {
		return fail("authenticator data too short")
	}
	rpIDHash := sha256.Sum256([]byte(rp.ID))
	if subtle.ConstantTimeCompare(ad[:32], rpIDHash[:]) != 1 {
		return fail("made for a different site")
	}
	flags := ad[32]
	if flags&flagUserPresent == 0 {
		return fail("user wasn't present")
	}
	if flags&flagUserVerified == 0 {
		return fail("user wasn't verified (fingerprint, face, or PIN required)")
	}
	counter := binary.BigEndian.Uint32(ad[33:37])

	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin"`
	}
	if err := json.Unmarshal(s.ClientDataJSON, &cd); err != nil {
		return fail("client data isn't JSON")
	}
	if cd.Type != "webauthn.get" {
		return fail("not an assertion (type %q)", cd.Type)
	}
	if cd.Origin != rp.Origin {
		return fail("made on %q, not %q", cd.Origin, rp.Origin)
	}
	if cd.CrossOrigin {
		return fail("made inside a cross-origin frame")
	}
	got, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cd.Challenge, "="))
	if err != nil || !bytes.Equal(got, challenge) {
		return fail("signed a different command")
	}

	key, err := webauthncose.ParsePublicKey(coseKey)
	if err != nil {
		return fail("stored public key is unusable: %v", err)
	}
	cdHash := sha256.Sum256(s.ClientDataJSON)
	data := append(append([]byte(nil), ad...), cdHash[:]...)
	ok, err := webauthncose.VerifySignature(key, data, s.Signature)
	if err != nil || !ok {
		return fail("signature doesn't match")
	}
	return counter, nil
}
