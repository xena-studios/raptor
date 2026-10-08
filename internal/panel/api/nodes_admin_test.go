package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Renaming and removing nodes: admins and owners only; removing needs a
// recent sign-in or confirmation, and tells the Panel to drop the node.
func TestRenameAndRemoveNode(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	var removed []string
	orgSvc := &orgs.Service{
		DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Now: clock,
		NodeRemoved: func(_ context.Context, id string) { removed = append(removed, id) },
	}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
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
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'rmnd1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	if _, err := bob.orgs.RenameNode(ctx, &panelv1.RenameNodeRequest{OrgId: org, NodeId: nodeID, Name: "mine"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member renaming: %v", err)
	}
	if _, err := alice.orgs.RenameNode(ctx, &panelv1.RenameNodeRequest{OrgId: org, NodeId: nodeID, Name: "  "}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an empty name: %v", err)
	}
	if _, err := alice.orgs.RenameNode(ctx, &panelv1.RenameNodeRequest{OrgId: org, NodeId: nodeID, Name: "Basement box"}); err != nil {
		t.Fatal(err)
	}
	list, _ := alice.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org})
	if len(list.GetNodes()) != 1 || list.GetNodes()[0].GetName() != "Basement box" {
		t.Fatalf("after renaming: %v", list)
	}

	if _, err := bob.orgs.RemoveNode(ctx, &panelv1.RemoveNodeRequest{OrgId: org, NodeId: nodeID}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member removing: %v", err)
	}
	// Long after signing in, removing needs "confirm it's you" first.
	now = now.Add(time.Hour)
	if _, err := alice.orgs.RemoveNode(ctx, &panelv1.RemoveNodeRequest{OrgId: org, NodeId: nodeID}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("without a recent confirmation: %v", err)
	}
	now = time.Now()
	alice2, _ := signUp("alice@example.com")
	if _, err := alice2.orgs.RemoveNode(ctx, &panelv1.RemoveNodeRequest{OrgId: org, NodeId: nodeID}); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != nodeID {
		t.Errorf("NodeRemoved: %v", removed)
	}
	list, _ = alice2.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org})
	if len(list.GetNodes()) != 0 {
		t.Errorf("a removed node is still listed: %v", list)
	}
	if _, err := alice2.orgs.RemoveNode(ctx, &panelv1.RemoveNodeRequest{OrgId: org, NodeId: nodeID}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("removing it again: %v", err)
	}
}
