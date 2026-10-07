package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

// wingsNode is a real Wings command executor behind the Panel: every
// grant and passkey signature is checked the way a node checks them.
type wingsNode struct{ x *command.Executor }

func (n wingsNode) Execute(ctx context.Context, _ string, raw []byte) (*nodev1.ExecuteResponse, error) {
	var e command.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	res, err := n.x.Execute(ctx, e)
	if err != nil {
		// Refused or failed on the node: an answer, as the link sends it.
		return &nodev1.ExecuteResponse{Error: err.Error()}, nil //nolint:nilerr // the node's answer, not a transport error
	}
	return &nodev1.ExecuteResponse{Result: res.Value, Duplicate: res.Duplicate}, nil
}

// Passkey-signed commands end to end, browser to Panel to Wings: the owner
// signs a delegation through the Panel, Wings trusts it, and the member's
// own passkey then approves exactly what was delegated, on that server only.
// Neither the Panel nor anyone else can widen it.
func TestDelegationEndToEnd(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}

	wdb, err := wstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wdb.Close() }()
	rp, err := command.RelyingPartyFor(appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	ran := map[string]int{}
	x := &command.Executor{DB: wdb, RP: rp, PanelKey: pub}
	x.Register("server.reinstall", command.Handler{Signed: command.Always, Run: func(_ context.Context, e command.Envelope) (any, error) {
		ran[e.ServerID]++
		return map[string]bool{"ok": true}, nil
	}})

	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc},
		Commands: &commands.Service{Auth: authSvc, Sender: wingsNode{x}, PanelKey: key}, AppOrigin: appOrigin,
	}))
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
	alice, aliceID := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'abcd1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	x.NodeID = nodeID
	for _, s := range []string{"s1", "s2"} {
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, $2, 'mc', 1, now(), now())`, nodeID, s); err != nil {
			t.Fatal(err)
		}
	}
	// Bob may reinstall s1 and s2 as far as the Panel is concerned: the
	// node's trusted keys are what limit him.
	perm, _ := perms.For("server.reinstall")
	for _, s := range []string{"s1", "s2"} {
		if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: s, UserId: bobID, Permissions: []string{perm}}); err != nil {
			t.Fatal(err)
		}
	}

	host := strings.TrimPrefix(appOrigin, "https://")
	passkey := func() *commandtest.Authenticator {
		a, err := commandtest.New("ES256", appOrigin, host, nil)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	alicePK, bobPK := passkey(), passkey()
	// Alice's passkey was pinned when the node linked (TestOwnerPin).
	if err := command.AddKey(ctx, wdb, command.KeyParams{CredentialID: alicePK.CredentialID, PublicKey: alicePK.COSE, UserID: aliceID, Role: "owner"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// signed is the web app: it builds the command, its passkey signs the
	// hash, and the Panel only adds its grant.
	signed := func(b *browser, user string, pk *commandtest.Authenticator, action, server string, params any) error {
		t.Helper()
		id, _ := uuid.NewV7()
		raw, _ := json.Marshal(params)
		env := nodecmd.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: user, Action: action, ServerID: server, Params: raw, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
		h, err := env.Hash()
		if err != nil {
			t.Fatal(err)
		}
		ad, cd, sig := pk.Assert(h)
		_, err = b.cmds.Execute(ctx, &panelv1.ExecuteRequest{
			NodeId: nodeID, Action: action, ServerId: server, ParamsJson: string(raw), CommandId: env.CommandID, ExpiresAt: env.ExpiresAt,
			Signature: &panelv1.CommandSignature{CredentialId: pk.CredentialID, AuthenticatorData: ad, ClientDataJson: cd, Signature: sig},
		})
		return err
	}

	// Before any delegation, bob's passkey approves nothing.
	if err := signed(bob, bobID, bobPK, "server.reinstall", "s1", map[string]any{}); err == nil {
		t.Fatal("an untrusted passkey's command ran")
	}
	// Nor can bob trust his own key: key changes need an owner's.
	delegation := command.KeyParams{CredentialID: bobPK.CredentialID, PublicKey: bobPK.COSE, UserID: bobID, Role: "delegate", ServerID: "s1", Actions: []string{"server.reinstall"}, ExpiresAt: time.Now().Add(time.Hour).Unix(), Name: "Bob's phone"}
	if err := signed(bob, bobID, bobPK, command.ActionKeysAdd, "", delegation); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a member adding a key: %v", err)
	}

	// Alice delegates reinstalls of s1 to bob's passkey, signed by hers.
	if err := signed(alice, aliceID, alicePK, command.ActionKeysAdd, "", delegation); err != nil {
		t.Fatalf("delegating: %v", err)
	}
	if err := signed(bob, bobID, bobPK, "server.reinstall", "s1", map[string]any{}); err != nil {
		t.Fatalf("delegated reinstall: %v", err)
	}
	if ran["s1"] != 1 {
		t.Errorf("reinstalls: %v", ran)
	}
	// Only s1: the Panel would allow s2, the node doesn't.
	if err := signed(bob, bobID, bobPK, "server.reinstall", "s2", map[string]any{}); err == nil {
		t.Error("the delegation reached another server")
	}
	// A command signed for bob can't be passed off as alice's: the Panel
	// puts the signed-in user in the grant, and the signature covers it.
	if err := signed(alice, bobID, bobPK, "server.reinstall", "s1", map[string]any{}); err == nil {
		t.Error("bob's signature worked under alice's session")
	}
	// Alice takes it back; bob's passkey stops working.
	if err := signed(alice, aliceID, alicePK, command.ActionKeysRemove, "", command.RemoveKeyParams{CredentialID: bobPK.CredentialID}); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if err := signed(bob, bobID, bobPK, "server.reinstall", "s1", map[string]any{}); err == nil {
		t.Error("a removed delegation still worked")
	}
	if ran["s1"] != 1 || ran["s2"] != 0 {
		t.Errorf("reinstalls: %v", ran)
	}
}
