package command

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

func fixedAuthenticator() *authenticator {
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	cose, err := webauthncbor.Marshal(webauthncose.OKPPublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: 1, Algorithm: int64(webauthncose.AlgEdDSA)},
		Curve:         6, XCoord: priv.Public().(ed25519.PublicKey),
	})
	if err != nil {
		panic(err)
	}
	return &authenticator{
		credID: []byte("fixed"), cose: cose, origin: testRP.Origin, rpID: testRP.ID, typ: "webauthn.get",
		flags: flagUserPresent | flagUserVerified,
		sign:  func(d []byte) []byte { return ed25519.Sign(priv, d) },
	}
}

// Random assertions never verify and never crash the parser. The fuzzer runs
// this in several processes, so the authenticator must be deterministic:
// Ed25519 from a fixed seed (Ed25519 signatures are deterministic too).
func FuzzVerifyAssertion(f *testing.F) {
	a := fixedAuthenticator()
	challenge := sha256.Sum256([]byte("command"))
	good := a.assert(challenge[:])
	f.Add(good.AuthenticatorData, good.ClientDataJSON, good.Signature, a.cose)
	f.Add([]byte{}, []byte("{}"), []byte{}, []byte{})
	f.Fuzz(func(t *testing.T, ad, cd, sig, key []byte) {
		s := PasskeySignature{AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
		_, err := verifyAssertion(key, s, challenge[:], testRP)
		genuine := string(ad) == string(good.AuthenticatorData) && string(cd) == string(good.ClientDataJSON) &&
			string(sig) == string(good.Signature) && string(key) == string(a.cose)
		if err == nil && !genuine {
			t.Fatalf("a forged assertion verified")
		}
	})
}

// Arbitrary envelopes are rejected cleanly, never crash, and never run.
func FuzzExecute(f *testing.F) {
	f.Add([]byte(`{"command_id":"0192f0a4-0000-7000-8000-000000000000","node_id":"node-1","user_id":"u","action":"server.delete","params":{"a":1},"expires_at":1800000100}`))
	f.Add([]byte(`{"action":"keys.add","params":{"public_key":"AAAA","role":"owner"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		fx := newFixture(t)
		var e Envelope
		if json.Unmarshal(data, &e) != nil {
			return
		}
		_, _ = fx.x.Execute(context.Background(), e) // no valid grant is possible
		if fx.runs.Load() != 0 {
			t.Fatal("a command without a valid grant ran")
		}
	})
}
