// Package commandtest provides a software passkey authenticator for tests:
// it produces real WebAuthn assertions, like a phone or security key would.
package commandtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Authenticator flags.
const (
	FlagUserPresent  = 0x01
	FlagUserVerified = 0x04
)

// Authenticator is a software passkey. The exported fields can be changed to
// produce invalid assertions in tests.
type Authenticator struct {
	CredentialID []byte
	COSE         []byte // the public key, as a relying party stores it
	Counter      uint32

	Origin      string
	RPID        string
	Type        string
	Flags       byte
	CrossOrigin bool

	sign func([]byte) []byte
}

// New creates an authenticator for alg ("ES256", "EdDSA", "RS256") on the
// given origin and RP ID. A nil seed uses a random key; a 32-byte seed makes
// an EdDSA authenticator deterministic (for fuzzing across processes).
func New(alg, origin, rpID string, seed []byte) (*Authenticator, error) {
	a := &Authenticator{Origin: origin, RPID: rpID, Type: "webauthn.get", Flags: FlagUserPresent | FlagUserVerified}
	a.CredentialID = make([]byte, 16)
	if seed != nil {
		copy(a.CredentialID, seed)
	} else if _, err := rand.Read(a.CredentialID); err != nil {
		return nil, err
	}
	var key any
	switch alg {
	case "ES256":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		key = webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: int64(webauthncose.AlgES256)},
			Curve:         1, XCoord: priv.X.FillBytes(make([]byte, 32)), YCoord: priv.Y.FillBytes(make([]byte, 32)),
		}
		a.sign = func(d []byte) []byte {
			h := sha256.Sum256(d)
			sig, _ := ecdsa.SignASN1(rand.Reader, priv, h[:])
			return sig
		}
	case "EdDSA":
		var priv ed25519.PrivateKey
		if seed != nil {
			priv = ed25519.NewKeyFromSeed(seed)
		} else {
			_, p, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return nil, err
			}
			priv = p
		}
		key = webauthncose.OKPPublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: 1, Algorithm: int64(webauthncose.AlgEdDSA)},
			Curve:         6, XCoord: priv.Public().(ed25519.PublicKey),
		}
		a.sign = func(d []byte) []byte { return ed25519.Sign(priv, d) }
	case "RS256":
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		key = webauthncose.RSAPublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: 3, Algorithm: int64(webauthncose.AlgRS256)},
			Modulus:       priv.N.Bytes(), Exponent: big.NewInt(int64(priv.E)).Bytes(),
		}
		a.sign = func(d []byte) []byte {
			h := sha256.Sum256(d)
			sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
			return sig
		}
	default:
		return nil, fmt.Errorf("unknown algorithm %q", alg)
	}
	cose, err := webauthncbor.Marshal(key)
	if err != nil {
		return nil, err
	}
	a.COSE = cose
	return a, nil
}

// Assert signs a challenge the way navigator.credentials.get does and
// returns authenticatorData, clientDataJSON, and the signature.
func (a *Authenticator) Assert(challenge []byte) (authData, clientData, sig []byte) {
	rp := sha256.Sum256([]byte(a.RPID))
	authData = append(rp[:], a.Flags)
	authData = binary.BigEndian.AppendUint32(authData, a.Counter)
	clientData, _ = json.Marshal(map[string]any{
		"type": a.Type, "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": a.Origin, "crossOrigin": a.CrossOrigin,
	})
	cdHash := sha256.Sum256(clientData)
	sig = a.sign(append(append([]byte(nil), authData...), cdHash[:]...))
	return authData, clientData, sig
}
