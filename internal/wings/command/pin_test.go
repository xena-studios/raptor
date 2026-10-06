package command

import (
	"context"
	"errors"
	"testing"

	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

func (f *fixture) pin(a *authenticator, token, user string) nodecmd.OwnerPin {
	f.t.Helper()
	p := nodecmd.OwnerPin{
		JoinTokenHash: nodecmd.JoinTokenHash(token), CredentialID: a.CredentialID, PublicKey: a.COSE, UserID: user, Name: "MacBook",
	}
	h, err := p.Hash()
	if err != nil {
		f.t.Fatal(err)
	}
	sig := a.assert(h)
	p.Signature = sig
	return p
}

func TestPinOwner(t *testing.T) {
	ctx := context.Background()
	const token = "rpt_join_abc"
	f := newFixture(t)
	owner := newAuthenticator(t, "ES256")

	// Things that must not be pinned.
	for what, change := range map[string]func(*nodecmd.OwnerPin, *string){
		"another join token": func(_ *nodecmd.OwnerPin, tok *string) { *tok = "rpt_join_other" },
		"a different key named": func(p *nodecmd.OwnerPin, _ *string) {
			p.PublicKey = newAuthenticator(t, "ES256").COSE
		},
		"another user named": func(p *nodecmd.OwnerPin, _ *string) { p.UserID = "someone-else" },
		"no signature":       func(p *nodecmd.OwnerPin, _ *string) { p.Signature = nil },
	} {
		p, tok := f.pin(owner, token, "alice"), token
		change(&p, &tok)
		if _, err := f.x.PinOwner(ctx, p, tok); err == nil {
			t.Errorf("%s: pinned", what)
		}
	}
	// Signed on another site (a phishing page, or another RP ID).
	phished := owner.clone()
	phished.Origin = "https://app.raptorpanel.net.evil.test"
	if _, err := f.x.PinOwner(ctx, f.pin(phished, token, "alice"), token); err == nil {
		t.Error("a pin signed on another origin was pinned")
	}
	if keys, _ := f.db.Read.ListTrustedKeys(ctx); len(keys) != 0 {
		t.Fatalf("keys after refusals: %v", keys)
	}

	fp, err := f.x.PinOwner(ctx, f.pin(owner, token, "alice"), token)
	if err != nil {
		t.Fatal(err)
	}
	if fp != KeyFingerprint(owner.COSE) {
		t.Errorf("fingerprint %s", fp)
	}
	// The pinned key now signs dangerous commands.
	e := f.sign(f.cmd("alice", "server.delete", "s1", nil), owner)
	if _, err := f.x.Execute(ctx, e); err != nil {
		t.Errorf("signed by the pinned key: %v", err)
	}
	// A pin never adds a second owner: that's keys.add, signed by an owner.
	other := newAuthenticator(t, "EdDSA")
	if _, err := f.x.PinOwner(ctx, f.pin(other, token, "mallory"), token); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("second pin: %v", err)
	}
}
