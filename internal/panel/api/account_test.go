package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

func TestAccountSettings(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
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
	ptr := func(s string) *string { return &s }

	alice, aliceID := signUp("alice@example.com")

	// Name and theme.
	res, err := alice.auth.UpdateProfile(ctx, &panelv1.UpdateProfileRequest{Name: ptr("  Alice   Liddell "), Theme: ptr("light")})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetUser().GetName() != "Alice Liddell" || res.GetUser().GetTheme() != "light" {
		t.Errorf("profile: %v", res.GetUser())
	}
	if _, err := alice.auth.UpdateProfile(ctx, &panelv1.UpdateProfileRequest{Theme: ptr("purple")}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown theme: %v", err)
	}
	if _, err := alice.auth.UpdateProfile(ctx, &panelv1.UpdateProfileRequest{Name: ptr(strings.Repeat("a", 65))}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a long name: %v", err)
	}
	me, _ := alice.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if me.GetUser().GetName() != "Alice Liddell" || me.GetUser().GetTheme() != "light" || me.GetUser().GetCreatedAt() == nil {
		t.Errorf("session after the update: %v", me.GetUser())
	}

	// Changing email: the new address gets a code; a taken one is refused.
	signUp("bob@example.com")
	if _, err := alice.auth.StartEmailChange(ctx, &panelv1.StartEmailChangeRequest{NewEmail: "bob@example.com"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a taken address: %v", err)
	}
	if _, err := alice.auth.StartEmailChange(ctx, &panelv1.StartEmailChangeRequest{NewEmail: "Alice@New.example"}); err != nil {
		t.Fatal(err)
	}
	sent := mail.lastMail(t)
	if !strings.HasPrefix(sent, "alice@new.example\n") {
		t.Fatalf("the code went to %q", strings.SplitN(sent, "\n", 2)[0])
	}
	code := codeRe.FindStringSubmatch(sent)[1]
	// The code is for alice's account: bob can't use it to take the address.
	bob, _ := signUp("bob@example.com")
	if _, err := bob.auth.FinishEmailChange(ctx, &panelv1.FinishEmailChangeRequest{NewEmail: "alice@new.example", Code: code}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("another account using the code: %v", err)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := alice.auth.FinishEmailChange(ctx, &panelv1.FinishEmailChangeRequest{NewEmail: "alice@new.example", Code: wrong}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a wrong code: %v", err)
	}
	changed, err := alice.auth.FinishEmailChange(ctx, &panelv1.FinishEmailChangeRequest{NewEmail: "alice@new.example", Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if changed.GetUser().GetEmail() != "alice@new.example" {
		t.Errorf("after the change: %v", changed.GetUser())
	}
	if notice := mail.lastMail(t); !strings.HasPrefix(notice, "alice@example.com\n") || !strings.Contains(notice, "alice@new.example") {
		t.Errorf("the old address wasn't told: %q", notice)
	}

	// Changing email needs a recent "confirm it's you".
	now = now.Add(time.Hour)
	if _, err := alice.auth.StartEmailChange(ctx, &panelv1.StartEmailChangeRequest{NewEmail: "alice@other.example"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("without a recent confirmation: %v", err)
	}
	if _, err := alice.auth.DeleteAccount(ctx, &panelv1.DeleteAccountRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("deleting without a recent confirmation: %v", err)
	}
	now = time.Now()

	// Deleting: refused while alice is the only owner of an org with others
	// in it, or with nodes.
	alice, _ = signUp("alice@new.example")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	var bobID string
	_ = db.QueryRow(ctx, "SELECT id FROM users WHERE email = 'bob@example.com'").Scan(&bobID)
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.auth.DeleteAccount(ctx, &panelv1.DeleteAccountRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "only owner") {
		t.Errorf("the only owner, with a member: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM org_members WHERE user_id = $1", bobID); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'acct1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.auth.DeleteAccount(ctx, &panelv1.DeleteAccountRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "nodes") {
		t.Errorf("an org with nodes: %v", err)
	}
	if _, err := alice.orgs.RemoveNode(ctx, &panelv1.RemoveNodeRequest{OrgId: org, NodeId: nodeID}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.InviteMember(ctx, &panelv1.InviteMemberRequest{OrgId: org, Email: "carol@example.com", Role: panelv1.Role_ROLE_MEMBER}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.auth.DeleteAccount(ctx, &panelv1.DeleteAccountRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("still signed in: %v", err)
	}
	var users, own, orgRows, pending int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", aliceID).Scan(&users)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE actor_id = $1 AND org_id IS NULL", aliceID).Scan(&own)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE org_id = $1", org).Scan(&orgRows)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM org_invitations WHERE org_id = $1 AND revoked_at IS NULL", org).Scan(&pending)
	if users != 0 || own != 0 || orgRows == 0 || pending != 0 {
		t.Errorf("after deleting: users %d, account events %d, org events %d, pending invitations %d", users, own, orgRows, pending)
	}
	// Signing in with the address again starts a new account.
	_, again := signUp("alice@new.example")
	if again == aliceID {
		t.Error("the deleted account came back")
	}
}
