package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

var inviteRe = regexp.MustCompile(`/invite#([A-Za-z0-9_-]+)`)

func TestOrgs(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	var now time.Time
	clock := func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Now: clock}
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
	invite := func(by *browser, org, email string, role panelv1.Role) (string, error) {
		t.Helper()
		if _, err := by.orgs.InviteMember(ctx, &panelv1.InviteMemberRequest{OrgId: org, Email: email, Role: role}); err != nil {
			return "", err
		}
		return inviteRe.FindStringSubmatch(mail.lastMail(t))[1], nil
	}
	accept := func(b *browser, token string) (*panelv1.AcceptInvitationResponse, error) {
		return b.orgs.AcceptInvitation(ctx, &panelv1.AcceptInvitationRequest{Token: token})
	}
	roles := func(b *browser, org string) map[string]panelv1.Role {
		t.Helper()
		res, err := b.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]panelv1.Role{}
		for _, m := range res.GetMembers() {
			out[m.GetEmail()] = m.GetRole()
		}
		return out
	}
	const (
		owner  = panelv1.Role_ROLE_OWNER
		admin  = panelv1.Role_ROLE_ADMIN
		member = panelv1.Role_ROLE_MEMBER
	)

	alice, aliceID := signUp("alice@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "  Acme  "})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if created.GetOrg().GetName() != "Acme" || created.GetOrg().GetRole() != owner {
		t.Errorf("created: %v", created)
	}
	if _, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: " "}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("blank name: %v", err)
	}

	// Outsiders can't tell the org exists.
	bob, bobID := signUp("bob@example.com")
	if _, err := bob.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("outsider listing members: %v", err)
	}
	if _, err := bob.orgs.RenameOrg(ctx, &panelv1.RenameOrgRequest{OrgId: org, Name: "Mine"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("outsider renaming: %v", err)
	}
	if _, err := newBrowser(t, srv, appOrigin).orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("signed out: %v", err)
	}

	// An invitation is for one address.
	token, err := invite(alice, org, "Bob@Example.com", admin)
	if err != nil {
		t.Fatal(err)
	}
	carol, carolID := signUp("carol@example.com")
	if _, err := accept(carol, token); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("someone else's invitation: %v", err)
	}
	if res, err := accept(bob, token); err != nil || res.GetOrg().GetRole() != admin {
		t.Fatalf("accept: %v, %v", res, err)
	}
	if _, err := accept(bob, token); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("invitation reused: %v", err)
	}
	list, err := bob.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{})
	if err != nil || len(list.GetOrgs()) != 1 || list.GetOrgs()[0].GetName() != "Acme" {
		t.Fatalf("bob's orgs: %v, %v", list, err)
	}

	// Admins invite up to their own role, and manage invitations.
	if _, err := invite(bob, org, "dave@example.com", owner); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("admin inviting an owner: %v", err)
	}
	daveToken, err := invite(bob, org, "dave@example.com", member)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := bob.orgs.ListInvitations(ctx, &panelv1.ListInvitationsRequest{OrgId: org})
	if err != nil || len(inv.GetInvitations()) != 1 || inv.GetInvitations()[0].GetEmail() != "dave@example.com" {
		t.Fatalf("invitations: %v, %v", inv, err)
	}
	if _, err := bob.orgs.RevokeInvitation(ctx, &panelv1.RevokeInvitationRequest{OrgId: org, InvitationId: inv.GetInvitations()[0].GetId()}); err != nil {
		t.Fatal(err)
	}
	dave, _ := signUp("dave@example.com")
	if _, err := accept(dave, daveToken); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("revoked invitation: %v", err)
	}
	// Invitations expire.
	daveToken, _ = invite(bob, org, "dave@example.com", member)
	now = time.Now().Add(orgs.InvitationTTL + time.Hour)
	if _, err := accept(dave, daveToken); connect.CodeOf(err) != connect.CodeUnauthenticated && connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("expired invitation: %v", err)
	}
	now = time.Time{}

	// Members see the org but can't change it.
	carolToken, _ := invite(alice, org, "carol@example.com", member)
	if _, err := accept(carol, carolToken); err != nil {
		t.Fatal(err)
	}
	if r := roles(carol, org); r["alice@example.com"] != owner || r["bob@example.com"] != admin || r["carol@example.com"] != member {
		t.Errorf("roles: %v", r)
	}
	if _, err := invite(carol, org, "eve@example.com", member); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member inviting: %v", err)
	}
	if _, err := carol.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: bobID}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member removing an admin: %v", err)
	}
	if _, err := carol.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member making a join token: %v", err)
	}
	// Only owners change roles.
	if _, err := bob.orgs.SetMemberRole(ctx, &panelv1.SetMemberRoleRequest{OrgId: org, UserId: bobID, Role: owner}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("admin promoting themselves: %v", err)
	}
	// Admins remove members, not other admins.
	if _, err := bob.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: aliceID}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("admin removing an owner: %v", err)
	}
	if _, err := bob.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: carolID}); err != nil {
		t.Errorf("admin removing a member: %v", err)
	}
	if _, err := carol.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("removed member: %v", err)
	}

	// An org always keeps an owner.
	if _, err := alice.orgs.SetMemberRole(ctx, &panelv1.SetMemberRoleRequest{OrgId: org, UserId: aliceID, Role: admin}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("last owner stepping down: %v", err)
	}
	if _, err := alice.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: aliceID}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("last owner leaving: %v", err)
	}
	if _, err := alice.orgs.SetMemberRole(ctx, &panelv1.SetMemberRoleRequest{OrgId: org, UserId: bobID, Role: owner}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: aliceID}); err != nil {
		t.Errorf("owner leaving with another owner: %v", err)
	}
	if r := roles(bob, org); len(r) != 1 || r["bob@example.com"] != owner {
		t.Errorf("after alice left: %v", r)
	}

	// Join tokens: admins and owners, with a recent re-auth.
	jt, err := bob.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box-1"})
	if err != nil || !strings.HasPrefix(jt.GetToken(), nodelink.JoinTokenPrefix) {
		t.Fatalf("join token: %v, %v", jt, err)
	}
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	if _, err := bob.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("join token without re-auth: %v", err)
	}
	now = time.Time{}

	// Two owners stepping down at once still leave one.
	q := store.New(db)
	for range 10 {
		o, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Race"})
		if err != nil {
			t.Fatal(err)
		}
		id := pgtype.UUID{Bytes: uuid.MustParse(o.GetOrg().GetId()), Valid: true}
		if err := q.AddOrgMember(ctx, store.AddOrgMemberParams{OrgID: id, UserID: pgtype.UUID{Bytes: uuid.MustParse(bobID), Valid: true}, Role: "owner"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, who := range []struct {
			b  *browser
			id string
		}{{alice, aliceID}, {bob, bobID}} {
			wg.Go(func() {
				_, _ = who.b.orgs.SetMemberRole(ctx, &panelv1.SetMemberRoleRequest{OrgId: o.GetOrg().GetId(), UserId: who.id, Role: member})
			})
		}
		wg.Wait()
		owners, err := q.LockOrgOwners(ctx, id)
		if err != nil || len(owners) != 1 {
			t.Fatalf("owners after a race: %v, %v", owners, err)
		}
		_, _ = q.RemoveOrgMember(ctx, store.RemoveOrgMemberParams{OrgID: id, UserID: owners[0]})
	}
}
