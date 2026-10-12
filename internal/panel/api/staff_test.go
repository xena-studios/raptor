package api

import (
	"bytes"
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
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// The staff panel: staff only, made from the server; a passkey confirmed
// recently to look and very recently to change; support only looks; what
// staff do is logged where staff can't change it, and in the account's or
// org's own log.
func TestStaff(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Now: clock}
	staffSvc := &staff.Service{DB: db, Auth: authSvc}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Staff: staffSvc, AppOrigin: appOrigin}))
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// A passkey on the account, and one confirmed by the session at a time.
	passkey := func(user string, at time.Time) {
		t.Helper()
		exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')
			ON CONFLICT DO NOTHING`, user, []byte(user))
		exec(`UPDATE sessions SET passkey_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, user, at)
	}

	admin, adminID := signUp("admin@raptor.example")
	support, supportID := signUp("support@raptor.example")
	alice, aliceID := signUp("alice@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()

	// Not staff: nothing, and GetStaffMe says so without an error.
	if me, err := alice.staff.GetStaffMe(ctx, &panelv1.GetStaffMeRequest{}); err != nil || me.GetRole() != "" {
		t.Fatalf("alice's role: %v, %v", me, err)
	}
	if _, err := alice.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer reading the overview: %v", err)
	}

	// Made staff on the server.
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no passkey") {
		t.Errorf("the CLI didn't say a passkey is needed: %s", out.String())
	}
	if err := staff.CLI(ctx, db, &out, []string{"add", "support@raptor.example", "support"}); err != nil {
		t.Fatal(err)
	}
	if err := staff.CLI(ctx, db, &out, []string{"add", "nobody@example.com", "admin"}); err == nil {
		t.Error("made staff of an account that doesn't exist")
	}

	// No passkey: closed. A stale one: confirm first.
	if _, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("staff without a passkey: %v", err)
	}
	passkey(adminID, now.Add(-13*time.Hour))
	if _, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a passkey from yesterday: %v", err)
	}
	passkey(adminID, now.Add(-time.Hour))
	if err := staffSvc.SampleStats(ctx); err != nil {
		t.Fatal(err)
	}
	ov, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if c := ov.GetCounts(); c.GetUsers() != 3 || c.GetOrgs() != 1 || len(ov.GetHistory()) != 1 || ov.GetRevenue().GetBillingLive() {
		t.Errorf("overview: %v", ov)
	}
	// The overview's tabs.
	if u, err := admin.staff.GetUserInsights(ctx, &panelv1.GetUserInsightsRequest{}); err != nil || u.GetUsers() != 3 || u.GetWithPasskey() != 1 || len(u.GetSigninMethods()) == 0 || len(u.GetLargestOrgs()) != 1 {
		t.Errorf("user insights: %v, %v", u, err)
	}
	if n, err := admin.staff.GetNodeInsights(ctx, &panelv1.GetNodeInsightsRequest{}); err != nil || n.GetNodes() != 0 {
		t.Errorf("node insights: %v, %v", n, err)
	}
	if b, err := admin.staff.GetBillingInsights(ctx, &panelv1.GetBillingInsightsRequest{}); err != nil || b.GetBillingLive() || b.GetCentsPerTb() != 1200 {
		t.Errorf("billing insights: %v, %v", b, err)
	}
	if i, err := admin.staff.GetInfrastructure(ctx, &panelv1.GetInfrastructureRequest{}); err != nil || i.GetDatabaseBytes() == 0 || len(i.GetLargestTables()) == 0 || i.GetVersion() == "" {
		t.Errorf("infrastructure: %v, %v", i, err)
	}
	if _, err := alice.staff.GetInfrastructure(ctx, &panelv1.GetInfrastructureRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer reading infrastructure: %v", err)
	}
	if me, _ := admin.staff.GetStaffMe(ctx, &panelv1.GetStaffMeRequest{}); me.GetRole() != staff.Admin || !me.GetPasskeyFresh() {
		t.Errorf("admin's role: %v", me)
	}

	// Changes need a passkey in the last 5 minutes.
	if _, err := admin.staff.SuspendOrg(ctx, &panelv1.SuspendOrgRequest{OrgId: org, Reason: "abuse report"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("suspending with an hour-old passkey: %v", err)
	}
	passkey(adminID, now)
	if _, err := admin.staff.SuspendOrg(ctx, &panelv1.SuspendOrgRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("suspending without a reason: %v", err)
	}
	if _, err := admin.staff.SuspendOrg(ctx, &panelv1.SuspendOrgRequest{OrgId: org, Reason: "abuse report"}); err != nil {
		t.Fatal(err)
	}
	// The org can't be managed, but it's listed, marked suspended.
	if _, err := alice.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("using a suspended org: %v", err)
	}
	if l, _ := alice.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{}); len(l.GetOrgs()) != 1 || !l.GetOrgs()[0].GetSuspended() {
		t.Errorf("listed: %v", l)
	}
	if _, err := admin.staff.UnsuspendOrg(ctx, &panelv1.UnsuspendOrgRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org}); err != nil {
		t.Errorf("after lifting it: %v", err)
	}

	// Support looks, and can't change anything.
	passkey(supportID, now)
	if u, err := support.staff.SearchUsers(ctx, &panelv1.SearchUsersRequest{Query: "alice"}); err != nil || len(u.GetUsers()) != 1 || u.GetUsers()[0].GetOrgs() != 1 {
		t.Errorf("support searching: %v, %v", u, err)
	}
	if _, err := support.staff.SuspendUser(ctx, &panelv1.SuspendUserRequest{UserId: aliceID, Reason: "x"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("support suspending: %v", err)
	}

	// Suspending an account signs it out and keeps it out.
	if _, err := admin.staff.SuspendUser(ctx, &panelv1.SuspendUserRequest{UserId: adminID, Reason: "x"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("suspending yourself: %v", err)
	}
	if _, err := admin.staff.SuspendUser(ctx, &panelv1.SuspendUserRequest{UserId: aliceID, Reason: "chargeback"}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{}); err == nil {
		t.Error("a suspended account is still signed in")
	}
	if _, err := alice.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err == nil {
		code, _ := mail.last(t)
		if _, err := alice.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
		}}); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("a suspended account signing in: %v", err)
		}
	}
	got, err := admin.staff.GetUser(ctx, &panelv1.GetUserRequest{UserId: aliceID})
	if err != nil || got.GetUser().GetSuspendedReason() != "chargeback" || len(got.GetMemberships()) != 1 {
		t.Errorf("alice, as staff see her: %v, %v", got, err)
	}

	// Announcements: admins publish, everyone signed in sees the live ones.
	if _, err := support.staff.SaveAnnouncement(ctx, &panelv1.SaveAnnouncementRequest{Announcement: &panelv1.Announcement{Level: "info", Title: "Hi"}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("support publishing: %v", err)
	}
	for _, bad := range []*panelv1.Announcement{
		{Level: "panic", Title: "x"},
		{Level: "info", Title: ""},
		{Level: "info", Title: "x", Link: "javascript:alert(1)"},
		{Level: "info", Title: "x", Link: "http://example.com"},
	} {
		if _, err := admin.staff.SaveAnnouncement(ctx, &panelv1.SaveAnnouncementRequest{Announcement: bad}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%v: %v", bad, err)
		}
	}
	saved, err := admin.staff.SaveAnnouncement(ctx, &panelv1.SaveAnnouncementRequest{Announcement: &panelv1.Announcement{
		Level: "maintenance", Title: "Panel maintenance tonight", Body: "<b>not html</b>", Link: "https://status.raptorpanel.net",
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = staff.CLI(ctx, db, &out, []string{"add", "support@raptor.example", "support"})
	bob, _ := signUp("bob@example.com")
	live, err := bob.staff.ListActiveAnnouncements(ctx, &panelv1.ListActiveAnnouncementsRequest{})
	if err != nil || len(live.GetAnnouncements()) != 1 || live.GetAnnouncements()[0].GetBody() != "<b>not html</b>" {
		t.Errorf("live announcements: %v, %v", live, err)
	}
	if _, err := admin.staff.EndAnnouncement(ctx, &panelv1.EndAnnouncementRequest{Id: saved.GetId()}); err != nil {
		t.Fatal(err)
	}
	if live, _ := bob.staff.ListActiveAnnouncements(ctx, &panelv1.ListActiveAnnouncementsRequest{}); len(live.GetAnnouncements()) != 0 {
		t.Errorf("an ended announcement is live: %v", live)
	}

	// The staff log has all of it, and can't be changed.
	log, err := admin.staff.ListStaffAudit(ctx, &panelv1.ListStaffAuditRequest{})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, e := range log.GetEntries() {
		actions[e.GetAction()] = true
	}
	for _, a := range []string{"staff.add", "org.suspend", "org.unsuspend", "user.suspend", "user.view", "announcement.create", "announcement.end"} {
		if !actions[a] {
			t.Errorf("%s isn't in the staff log: %v", a, actions)
		}
	}
	if _, err := db.Exec(ctx, "UPDATE staff_audit SET action = 'nothing'"); err == nil {
		t.Error("the staff log was changed")
	}
	if _, err := db.Exec(ctx, "DELETE FROM staff_audit"); err == nil {
		t.Error("the staff log was emptied")
	}
	// And the org's own log says staff did it.
	var actor string
	if err := db.QueryRow(ctx, "SELECT actor FROM audit_log WHERE action = 'org.suspend'").Scan(&actor); err != nil || actor != "staff" {
		t.Errorf("the org's log: %q, %v", actor, err)
	}
}
