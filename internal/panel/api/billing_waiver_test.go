package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// Billing waivers (DECISIONS #334): only admins give them, with a fresh
// passkey and a reason the org never sees; seats are the nodes left; an
// org whose nodes are all free is in good standing without a plan, and a
// plan it has ends with the period (and carries on when nodes are charged
// again, unless the owner cancelled it); an end date ends it, with emails.
func TestBillingWaivers(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	polar := &fakePolar{subs: map[string]map[string]any{}, invoices: map[string]bool{}}
	polarSrv := httptest.NewServer(polar)
	defer polarSrv.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	bill := &billing.Service{
		DB: db, Polar: &billing.Polar{Token: "polar_oat_test", API: polarSrv.URL}, ProductID: "prod1",
		WebhookSecret: testWebhookSecret, AppURL: appOrigin, Mailer: mail, Now: clock,
		Log: slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Billing: bill, Now: clock}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: orgSvc, Billing: bill, AppOrigin: appOrigin,
		Staff: &staff.Service{DB: db, Auth: authSvc, BillingLive: bill.Enabled},
	}))
	defer srv.Close()
	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: email, Code: code}}})
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
	sweep := func() {
		t.Helper()
		if err := bill.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	passkey := func(user string, at time.Time) {
		t.Helper()
		exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')
			ON CONFLICT DO NOTHING`, user, []byte(user))
		exec(`UPDATE sessions SET passkey_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, user, at)
	}
	emails := func(s string) int {
		mail.mu.Lock()
		defer mail.mu.Unlock()
		n := 0
		for _, m := range mail.mail {
			if strings.HasPrefix(m, "alice@example.com\n") && strings.Contains(m, s) {
				n++
			}
		}
		return n
	}

	alice, _ := signUp("alice@example.com")
	admin, adminID := signUp("admin@raptor.example")
	support, supportID := signUp("support@raptor.example")
	var out bytes.Buffer
	for _, s := range [][]string{{"add", "admin@raptor.example", "admin"}, {"add", "support@raptor.example", "support"}} {
		if err := staff.CLI(ctx, db, &out, s); err != nil {
			t.Fatal(err)
		}
	}
	passkey(adminID, now.Add(-time.Hour))
	passkey(supportID, now)
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	get := func() *panelv1.GetBillingResponse {
		t.Helper()
		res, err := alice.bill.GetBilling(ctx, &panelv1.GetBillingRequest{OrgId: org})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	newNode := func() string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waive := func(req *panelv1.SetBillingWaiverRequest) {
		t.Helper()
		req.OrgId = org
		if req.Reason == "" {
			req.Reason = "a partner"
		}
		if _, err := admin.staff.SetBillingWaiver(ctx, req); err != nil {
			t.Fatal(err)
		}
	}

	// Who may: admins, with a passkey in the last 5 minutes; not support,
	// and not the org's own owner.
	all := &panelv1.SetBillingWaiverRequest{OrgId: org, AllNodes: true, Reason: "the owner's own org"}
	if _, err := alice.staff.SetBillingWaiver(ctx, all); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer waiving their own bill: %v", err)
	}
	if _, err := support.staff.SetBillingWaiver(ctx, all); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("support giving a waiver: %v", err)
	}
	if _, err := admin.staff.SetBillingWaiver(ctx, all); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("an admin with an hour-old passkey: %v", err)
	}
	if _, err := admin.staff.EndBillingWaiver(ctx, &panelv1.EndBillingWaiverRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("ending with an hour-old passkey: %v", err)
	}
	passkey(adminID, now)
	for name, req := range map[string]*panelv1.SetBillingWaiverRequest{
		"no reason":    {OrgId: org, AllNodes: true},
		"nothing free": {OrgId: org, Reason: "x"},
		"too many":     {OrgId: org, FreeNodes: 10001, Reason: "x"},
		"ended":        {OrgId: org, AllNodes: true, Reason: "x", EndsAt: timestamppb.New(now.Add(-time.Minute))},
	} {
		if _, err := admin.staff.SetBillingWaiver(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := admin.staff.SetBillingWaiver(ctx, &panelv1.SetBillingWaiverRequest{OrgId: "00000000-0000-4000-8000-000000000000", AllNodes: true, Reason: "x"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("no such org: %v", err)
	}

	// Two nodes and no plan: unpaid. Every node free: in good standing.
	newNode()
	newNode()
	sweep()
	if b := get(); b.GetReadOnlyAt() == nil {
		t.Fatalf("two nodes without a plan: %v", b)
	}
	waive(&panelv1.SetBillingWaiverRequest{AllNodes: true, Reason: "the owner's own org"})
	if b := get(); b.GetReadOnlyAt() != nil || !b.GetWaiver().GetAllNodes() || b.GetFreeNodes() != 2 || !b.GetFreeNextNode() {
		t.Errorf("all nodes free, before the sweep: %v", b)
	}
	sweep()
	if b := get(); b.GetReadOnlyAt() != nil || b.GetReadOnly() {
		t.Errorf("all nodes free: %v", b)
	}
	var unpaid bool
	_ = db.QueryRow(ctx, "SELECT unpaid_since IS NOT NULL FROM billing_accounts WHERE org_id = $1", org).Scan(&unpaid)
	if unpaid {
		t.Error("the sweep left the org unpaid")
	}
	if _, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box"}); err != nil {
		t.Errorf("a join token with every node free: %v", err)
	}
	if _, err := alice.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a plan with every node free: %v", err)
	}

	// Some free, no plan: the rest still need one.
	waive(&panelv1.SetBillingWaiverRequest{FreeNodes: 1})
	sweep()
	if b := get(); b.GetReadOnlyAt() == nil || b.GetFreeNodes() != 1 || b.GetFreeNextNode() {
		t.Errorf("one of two nodes free, no plan: %v", b)
	}
	if _, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a join token for a node that would be charged, without a plan: %v", err)
	}
	if _, err := alice.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	if c := polar.checkouts[len(polar.checkouts)-1]; c["seats"] != float64(1) || c["allow_trial"] != true {
		t.Errorf("checkout for one charged node: %v", c)
	}

	// The plan: seats are the nodes less the free ones.
	polar.set("sub1", map[string]any{
		"status": "active", "seats": 1, "customer": map[string]any{"id": "cus1", "external_id": org},
	})
	if code := deliver(t, srv, "w1", "subscription.created", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	sweep()
	if b := get(); b.GetReadOnlyAt() != nil || b.GetSeats() != 1 {
		t.Errorf("a plan for the charged node: %v", b)
	}
	newNode()
	sweep()
	if b := get(); b.GetSeats() != 2 || b.GetNodes() != 3 {
		t.Errorf("a third node, one free: %v (patches %v)", b, polar.patches)
	}

	// Every node free: the plan ends with the period; one charged again: it
	// carries on.
	waive(&panelv1.SetBillingWaiverRequest{FreeNodes: 3})
	sweep()
	if b := get(); !b.GetCancelAtPeriodEnd() || b.GetSeats() != 2 {
		t.Errorf("every node free: %v", b)
	}
	waive(&panelv1.SetBillingWaiverRequest{FreeNodes: 2})
	sweep()
	if b := get(); b.GetCancelAtPeriodEnd() || b.GetSeats() != 1 {
		t.Errorf("one node charged again: %v", b)
	}

	// The owner's own cancellation stays, whatever the waiver does.
	if _, err := alice.bill.CancelPlan(ctx, &panelv1.CancelPlanRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	waive(&panelv1.SetBillingWaiverRequest{AllNodes: true})
	sweep()
	waive(&panelv1.SetBillingWaiverRequest{FreeNodes: 1})
	sweep()
	if b := get(); !b.GetCancelAtPeriodEnd() || b.GetSeats() != 2 {
		t.Errorf("the sweep undid the owner's cancellation: %v", b)
	}
	if _, err := alice.bill.CancelPlan(ctx, &panelv1.CancelPlanRequest{OrgId: org, Resume: true}); err != nil {
		t.Fatal(err)
	}

	// Staff see the waiver and its reason; owners see what it gives.
	waive(&panelv1.SetBillingWaiverRequest{AllNodes: true, Storage: true, Reason: "beta tester, ticket 12", EndsAt: timestamppb.New(now.Add(48 * time.Hour))})
	sweep()
	so, err := admin.staff.GetStaffOrg(ctx, &panelv1.GetStaffOrgRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	if w := so.GetWaiver(); w == nil || !w.GetAllNodes() || !w.GetStorage() || w.GetReason() != "beta tester, ticket 12" ||
		w.GetCreatedBy() != "admin@raptor.example" || w.GetEndsAt() == nil || so.GetPlanStatus() != "active" {
		t.Errorf("the staff org page: %v", so)
	}
	if b := get(); !b.GetWaiver().GetStorage() || b.GetWaiver().GetEndsAt() == nil || !b.GetCancelAtPeriodEnd() {
		t.Errorf("the owner's billing page: %v", b)
	}
	var meta string
	if err := db.QueryRow(ctx, "SELECT metadata::text FROM audit_log WHERE org_id = $1 AND action = 'billing.waiver.change' AND actor = 'staff' ORDER BY at DESC LIMIT 1", org).Scan(&meta); err != nil {
		t.Fatalf("the org's own log: %v", err)
	}
	if strings.Contains(meta, "ticket") {
		t.Errorf("the org's log has staff's reason: %s", meta)
	}
	// Overview: waived orgs and nodes, and no recurring revenue from a plan
	// whose nodes are all free.
	ins, err := admin.staff.GetBillingInsights(ctx, &panelv1.GetBillingInsightsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if ins.GetWaivedOrgs() != 1 || ins.GetWaivedNodes() != 3 || ins.GetMrrCents() != 0 {
		t.Errorf("billing insights: waived %d orgs, %d nodes, MRR %d", ins.GetWaivedOrgs(), ins.GetWaivedNodes(), ins.GetMrrCents())
	}
	ov, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r := ov.GetRevenue(); r.GetWaivedOrgs() != 1 || r.GetWaivedNodes() != 3 || r.GetMrrCents() != 0 {
		t.Errorf("overview revenue: %v", r)
	}

	// Within 3 days of its end: one reminder. At the end: over, an email,
	// and the nodes charged again.
	if n := emails("Raptor gives your org"); n != 1 {
		t.Errorf("reminders: %d", n)
	}
	sweep()
	if n := emails("Raptor gives your org"); n != 1 {
		t.Errorf("reminders after another sweep: %d", n)
	}
	now = now.Add(49 * time.Hour)
	sweep()
	if n := emails("That's ended"); n != 1 {
		t.Errorf("the ended email: %d (%q)", n, mail.mail)
	}
	sweep()
	if n := emails("That's ended"); n != 1 {
		t.Errorf("the ended email, again: %d", n)
	}
	if b := get(); b.GetWaiver() != nil || b.GetSeats() != 3 || b.GetCancelAtPeriodEnd() {
		t.Errorf("after the waiver ended: %v", b)
	}
	var kinds []string
	rows, _ := db.Query(ctx, "SELECT kind FROM billing_ledger WHERE org_id = $1 AND kind LIKE 'waiver.%' ORDER BY at", org)
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	rows.Close()
	if len(kinds) < 3 || kinds[0] != "waiver.set" || kinds[len(kinds)-1] != "waiver.ended" {
		t.Errorf("the ledger: %v", kinds)
	}

	// Ending one by hand.
	now = time.Now().Add(50 * time.Hour)
	passkey(adminID, now)
	waive(&panelv1.SetBillingWaiverRequest{FreeNodes: 1})
	if _, err := admin.staff.EndBillingWaiver(ctx, &panelv1.EndBillingWaiverRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.staff.EndBillingWaiver(ctx, &panelv1.EndBillingWaiverRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("ending it twice: %v", err)
	}
	if b := get(); b.GetWaiver() != nil {
		t.Errorf("ended by hand: %v", b)
	}
}
