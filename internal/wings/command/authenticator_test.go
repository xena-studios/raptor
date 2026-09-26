package command

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
	"math/big"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// authenticator is a software passkey: it produces real WebAuthn assertions,
// like a phone or security key would.
type authenticator struct {
	credID  []byte
	cose    []byte
	sign    func(data []byte) []byte
	counter uint32

	// Knobs for negative tests.
	origin      string
	rpID        string
	typ         string
	flags       byte
	crossOrigin bool
}

func newAuthenticator(t *testing.T, alg string) *authenticator {
	t.Helper()
	a := &authenticator{credID: randBytes(t, 16), origin: testRP.Origin, rpID: testRP.ID, typ: "webauthn.get", flags: flagUserPresent | flagUserVerified}
	var key any
	switch alg {
	case "ES256":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		x, y := priv.X.FillBytes(make([]byte, 32)), priv.Y.FillBytes(make([]byte, 32))
		key = webauthncose.EC2PublicKeyData{PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: int64(webauthncose.AlgES256)}, Curve: 1, XCoord: x, YCoord: y}
		a.sign = func(d []byte) []byte {
			h := sha256.Sum256(d)
			sig, err := ecdsa.SignASN1(rand.Reader, priv, h[:])
			if err != nil {
				t.Fatal(err)
			}
			return sig
		}
	case "EdDSA":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key = webauthncose.OKPPublicKeyData{PublicKeyData: webauthncose.PublicKeyData{KeyType: 1, Algorithm: int64(webauthncose.AlgEdDSA)}, Curve: 6, XCoord: pub}
		a.sign = func(d []byte) []byte { return ed25519.Sign(priv, d) }
	case "RS256":
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		key = webauthncose.RSAPublicKeyData{PublicKeyData: webauthncose.PublicKeyData{KeyType: 3, Algorithm: int64(webauthncose.AlgRS256)},
			Modulus: priv.N.Bytes(), Exponent: big.NewInt(int64(priv.E)).Bytes()}
		a.sign = func(d []byte) []byte {
			h := sha256.Sum256(d)
			sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
			if err != nil {
				t.Fatal(err)
			}
			return sig
		}
	default:
		t.Fatalf("unknown alg %s", alg)
	}
	cose, err := webauthncbor.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	a.cose = cose
	return a
}

// assert signs a challenge the way navigator.credentials.get does.
func (a *authenticator) assert(challenge []byte) *PasskeySignature {
	rp := sha256.Sum256([]byte(a.rpID))
	ad := append(rp[:], a.flags)
	ad = binary.BigEndian.AppendUint32(ad, a.counter)
	cd, _ := json.Marshal(map[string]any{
		"type": a.typ, "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": a.origin, "crossOrigin": a.crossOrigin,
	})
	cdHash := sha256.Sum256(cd)
	sig := a.sign(append(append([]byte(nil), ad...), cdHash[:]...))
	return &PasskeySignature{CredentialID: a.credID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
}

func randBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
