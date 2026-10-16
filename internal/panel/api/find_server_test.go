package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// A server's short ID finds it and its node, for the app's URLs; members
// only find servers they have access to, and a short ID two servers share
// finds nothing.
func TestFindServer(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
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
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	newNode := func() string {
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	addServer := func(node, id string) {
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, $2, 'mc', 1, now(), now())`, node, id); err != nil {
			t.Fatal(err)
		}
	}
	n1, n2 := newNode(), newNode()
	smp := "0192f0a4-0000-7000-8000-0000abcdef12"
	addServer(n1, smp)
	// Two servers whose IDs end the same, on different nodes.
	addServer(n1, "0192f0a4-0000-7000-8000-000000000dup")
	addServer(n2, "0192f0a5-0000-7000-8000-000000000dup")

	find := func(b *browser, ref string) (*panelv1.FindServerResponse, error) {
		return b.orgs.FindServer(ctx, &panelv1.FindServerRequest{OrgId: org, Server: ref})
	}
	for _, ref := range []string{"0000abcdef12", smp, "0000ABCDEF12"} {
		if r, err := find(alice, ref); err != nil || r.GetNodeId() != n1 || r.GetServerId() != smp {
			t.Errorf("%s: %v, %v", ref, r, err)
		}
	}
	for what, ref := range map[string]string{"shared": "000000000dup", "unknown": "ffffffffffff", "empty": ""} {
		if _, err := find(alice, ref); connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("%s: %v", what, err)
		}
	}
	// Bob, a member, only finds what he's been given.
	if _, err := find(bob, "0000abcdef12"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a member without access: %v", err)
	}
	if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: n1, ServerId: smp, UserId: bobID, Permissions: []string{"console.read"}}); err != nil {
		t.Fatal(err)
	}
	if r, err := find(bob, "0000abcdef12"); err != nil || r.GetServerId() != smp {
		t.Errorf("a member with access: %v, %v", r, err)
	}
}
