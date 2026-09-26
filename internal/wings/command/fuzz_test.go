package command

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
)

func fixedAuthenticator() *authenticator {
	a, err := commandtest.New("EdDSA", testRP.Origin, testRP.ID, make([]byte, 32))
	if err != nil {
		panic(err)
	}
	return &authenticator{a}
}

// Random assertions never verify and never crash the parser. The fuzzer runs
// this in several processes, so the authenticator must be deterministic:
// Ed25519 from a fixed seed (Ed25519 signatures are deterministic too).
func FuzzVerifyAssertion(f *testing.F) {
	a := fixedAuthenticator()
	challenge := sha256.Sum256([]byte("command"))
	good := a.assert(challenge[:])
	f.Add(good.AuthenticatorData, good.ClientDataJSON, good.Signature, a.COSE)
	f.Add([]byte{}, []byte("{}"), []byte{}, []byte{})
	f.Fuzz(func(t *testing.T, ad, cd, sig, key []byte) {
		s := PasskeySignature{AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
		_, err := verifyAssertion(key, s, challenge[:], testRP)
		genuine := string(ad) == string(good.AuthenticatorData) && string(cd) == string(good.ClientDataJSON) &&
			string(sig) == string(good.Signature) && string(key) == string(a.COSE)
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
