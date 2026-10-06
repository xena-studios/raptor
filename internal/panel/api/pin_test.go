package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/command"
	wingsstore "github.com/xena-studios/raptor/internal/wings/store"
)

// signPin is the browser's side: the passkey signs a statement naming the
// join token and itself.
func signPin(t *testing.T, p *passkey, token, userID string) []byte {
	t.Helper()
	pin := nodecmd.OwnerPin{
		JoinTokenHash: nodecmd.JoinTokenHash(token), CredentialID: p.CredentialID, PublicKey: p.COSE, UserID: userID, Name: "Laptop",
	}
	h, err := pin.Hash()
	if err != nil {
		t.Fatal(err)
	}
	ad, cd, sig := p.Assert(h)
	pin.Signature = &nodecmd.PasskeySignature{CredentialID: p.CredentialID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
	raw, _ := json.Marshal(pin)
	return raw
}

// A node trusts its owner's passkey from the moment it links: signed in the
// browser, stored with the join token by the Panel, handed over at
// enrollment, and checked and pinned by Wings.
func TestOwnerPin(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	wa, err := auth.NewWebAuthn(appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	_, panelKey, _ := ed25519.GenerateKey(rand.Reader)
	reg := &nodes.Registry{DB: db, PanelKey: panelKey}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, WebAuthn: wa}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc, Registry: reg}, Nodes: reg, AppOrigin: appOrigin}))
	defer srv.Close()

	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return b, res.GetUser().GetId()
	}
	addPasskey := func(b *browser) *passkey {
		t.Helper()
		p := newPasskey(t)
		begin, err := b.auth.BeginPasskeyRegistration(ctx, &panelv1.BeginPasskeyRegistrationRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.auth.FinishPasskeyRegistration(ctx, &panelv1.FinishPasskeyRegistrationRequest{Answer: p.create(t, begin.GetChallenge()), Name: "Laptop"}); err != nil {
			t.Fatal(err)
		}
		return p
	}

	alice, aliceID := signUp("alice@example.com")
	laptop := addPasskey(alice)
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	jt, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	token := jt.GetToken()
	pin := func(b *browser, raw []byte, tok string) error {
		_, err := b.orgs.PinJoinToken(ctx, &panelv1.PinJoinTokenRequest{OrgId: org, Token: tok, PinJson: string(raw)})
		return err
	}

	// Someone else's passkey can't be pinned for alice, nor a pin for
	// another token, nor alice's passkey under bob's name.
	bob, bobID := signUp("bob@example.com")
	bobKey := addPasskey(bob)
	if err := pin(alice, signPin(t, bobKey, token, bobID), token); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("someone else's passkey: %v", err)
	}
	// Bob's key, claiming to be alice's.
	if err := pin(alice, signPin(t, bobKey, token, aliceID), token); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("someone else's passkey under alice's name: %v", err)
	}
	if err := pin(alice, signPin(t, laptop, "rpt_join_other", aliceID), token); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a pin for another token: %v", err)
	}
	if err := pin(bob, signPin(t, laptop, token, aliceID), token); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider pinning: %v", err)
	}
	if err := pin(alice, signPin(t, laptop, token, aliceID), token); err != nil {
		t.Fatal(err)
	}
	if err := pin(alice, signPin(t, laptop, token, aliceID), token); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("pinned twice: %v", err)
	}

	// The node enrolls and gets the pin back.
	nodePub, nodeKey, _ := ed25519.GenerateKey(rand.Reader)
	payload, err := nodelink.EnrollPayload(token, nodePub)
	if err != nil {
		t.Fatal(err)
	}
	res, err := reg.Enroll(ctx, &nodev1.EnrollRequest{Token: token, PublicKey: nodePub, Signature: ed25519.Sign(nodeKey, payload), Name: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetOwnerPin()) == 0 {
		t.Fatal("no owner pin at enrollment")
	}

	// Wings checks it with its own relying party and trusts the key.
	wdb, err := wingsstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wdb.Close() }()
	rp, err := command.RelyingPartyFor(appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	x := &command.Executor{DB: wdb, NodeID: res.GetNodeId(), RP: rp}
	var got nodecmd.OwnerPin
	if err := json.Unmarshal(res.GetOwnerPin(), &got); err != nil {
		t.Fatal(err)
	}
	fp, err := x.PinOwner(ctx, got, token)
	if err != nil {
		t.Fatal(err)
	}
	if fp != command.KeyFingerprint(laptop.COSE) {
		t.Errorf("fingerprint %s", fp)
	}
	keys, err := command.ListKeys(ctx, wdb)
	if err != nil || len(keys) != 1 || keys[0].Role != "owner" || keys[0].UserID != aliceID {
		t.Fatalf("trusted keys: %+v, %v", keys, err)
	}
}
