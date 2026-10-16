package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Owners delete an org once its nodes are gone; everyone is out of it,
// and its invitations and join tokens stop working.
func TestDeleteOrg(t *testing.T) {
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
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')", org, bobID); err != nil {
		t.Fatal(err)
	}
	del := func(b *browser) error {
		_, err := b.orgs.DeleteOrg(ctx, &panelv1.DeleteOrgRequest{OrgId: org})
		return err
	}

	if err := del(bob); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("an admin deleting: %v", err)
	}
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if err := del(alice); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "1 node:") {
		t.Errorf("with a node: %v", err)
	}
	// Removed (as RemoveNode leaves it).
	if _, err := db.Exec(ctx, "UPDATE nodes SET deleted_at = now() WHERE id = $1", node); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.InviteMember(ctx, &panelv1.InviteMemberRequest{OrgId: org, Email: "carol@example.com", Role: panelv1.Role_ROLE_MEMBER}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO join_tokens (org_id, token_hash, expires_at) VALUES ($1, 'h', now() + interval '1 hour')", org); err != nil {
		t.Fatal(err)
	}
	if err := del(alice); err != nil {
		t.Fatal(err)
	}

	for who, b := range map[string]*browser{"alice": alice, "bob": bob} {
		list, err := b.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{})
		if err != nil || len(list.GetOrgs()) != 0 {
			t.Errorf("%s's orgs after: %v, %v", who, list, err)
		}
	}
	var deleted bool
	var pending, usable int
	_ = db.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM orgs WHERE id = $1", org).Scan(&deleted)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM org_invitations WHERE org_id = $1 AND revoked_at IS NULL AND accepted_at IS NULL", org).Scan(&pending)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM join_tokens WHERE org_id = $1 AND used_at IS NULL AND expires_at > now()", org).Scan(&usable)
	if !deleted || pending != 0 || usable != 0 {
		t.Errorf("after: deleted %v, %d invitations, %d join tokens", deleted, pending, usable)
	}
	var logged int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE org_id = $1 AND action = 'org.delete'", org).Scan(&logged)
	if logged != 1 {
		t.Errorf("%d org.delete entries", logged)
	}
	// It's gone for alice too: nothing more can be done in it.
	if err := del(alice); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("deleting again: %v", err)
	}
}
