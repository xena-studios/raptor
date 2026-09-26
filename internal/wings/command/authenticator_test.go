package command

import (
	"testing"

	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
)

// authenticator adapts commandtest.Authenticator to this package's types.
type authenticator struct{ *commandtest.Authenticator }

func newAuthenticator(t *testing.T, alg string) *authenticator {
	t.Helper()
	a, err := commandtest.New(alg, testRP.Origin, testRP.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{a}
}

// clone copies the authenticator so a test can change its knobs.
func (a *authenticator) clone() *authenticator {
	c := *a.Authenticator
	return &authenticator{&c}
}

func (a *authenticator) assert(challenge []byte) *PasskeySignature {
	ad, cd, sig := a.Assert(challenge)
	return &PasskeySignature{CredentialID: a.CredentialID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
}
