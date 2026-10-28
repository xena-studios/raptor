package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// fakePolar is the part of Polar's API billing uses.
type fakePolar struct {
	mu        sync.Mutex
	subs      map[string]map[string]any
	checkouts []map[string]any
	patches   []map[string]any
	portals   int
	// The customer's side: saved cards, the default, and orders.
	methods  []map[string]any
	defaultM string
	orders   []map[string]any
	invoices map[string]bool
	// Usage events ingested (each external ID once), and whether ingesting
	// fails.
	events     []map[string]any
	ingestDown bool
}

// patchKinds are Polar's subscription updates: one kind at a time.
var patchKinds = [][]string{
	{"metadata", "product_id", "proration_behavior", "discount_id", "trial_end"},
	{"seats", "proration_behavior"},
	{"cancel_at_period_end", "customer_cancellation_reason", "customer_cancellation_comment"},
}

func onePatchKind(body map[string]any) bool {
	for _, kind := range patchKinds {
		ok := true
		for k := range body {
			ok = ok && slices.Contains(kind, k)
		}
		if ok {
			return true
		}
	}
	return false
}

// portal is the customer portal API, with a customer session's token.
func (f *fakePolar) portal(w http.ResponseWriter, r *http.Request, body map[string]any, reply func(any)) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer polar_cst_") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	const pm = "/v1/customer-portal/customers/me/payment-methods"
	path := r.URL.Path
	order := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/customer-portal/orders/"), "/invoice")
	switch {
	case r.Method == http.MethodGet && path == "/v1/customer-portal/customers/me":
		reply(map[string]any{"id": "cus1", "default_payment_method_id": f.defaultM})
	case r.Method == http.MethodPatch && path == "/v1/customer-portal/customers/me":
		f.defaultM, _ = body["default_payment_method_id"].(string)
		reply(map[string]any{"id": "cus1", "default_payment_method_id": f.defaultM})
	case r.Method == http.MethodGet && path == pm:
		reply(map[string]any{"items": f.methods})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, pm+"/"):
		id := strings.TrimPrefix(path, pm+"/")
		if id == f.defaultM {
			w.WriteHeader(http.StatusBadRequest)
			reply(map[string]any{"error": "PaymentMethodInUseByActiveSubscription", "detail": "in use"})
			return
		}
		f.methods = slices.DeleteFunc(f.methods, func(m map[string]any) bool { return m["id"] == id })
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && path == "/v1/customer-portal/orders/":
		reply(map[string]any{"items": f.orders})
	case strings.HasSuffix(path, "/invoice") && r.Method == http.MethodPost:
		f.invoices[order] = true
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(path, "/invoice") && f.invoices[order]:
		reply(map[string]any{"url": "https://polar.test/invoice/" + order + ".pdf"})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/customer-portal/orders/"):
		for _, o := range f.orders {
			if o["id"] == order {
				o["is_invoice_generated"] = f.invoices[order]
				reply(o)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		reply(map[string]any{"error": "ResourceNotFound"})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePolar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if strings.HasPrefix(r.URL.Path, "/v1/customer-portal/") {
		f.portal(w, r, body, reply)
		return
	}
	if r.Header.Get("Authorization") != "Bearer polar_oat_test" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkouts/":
		f.checkouts = append(f.checkouts, body)
		reply(map[string]any{"id": "co1", "url": "https://polar.test/checkout/co1"})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customer-sessions/":
		f.portals++
		reply(map[string]any{
			"token": fmt.Sprintf("polar_cst_%d", f.portals), "expires_at": time.Now().Add(time.Hour).UTC(),
			"customer_portal_url": "https://polar.test/portal",
		})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/events/ingest":
		if f.ingestDown {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		in, dups := 0, 0
		evs, _ := body["events"].([]any)
		for _, e := range evs {
			ev, _ := e.(map[string]any)
			if slices.ContainsFunc(f.events, func(x map[string]any) bool { return x["external_id"] == ev["external_id"] }) {
				dups++
				continue
			}
			f.events = append(f.events, ev)
			in++
		}
		reply(map[string]any{"inserted": in, "duplicates": dups})
	case r.Method == http.MethodGet && f.subs[id] != nil:
		reply(f.subs[id])
	case r.Method == http.MethodPatch && f.subs[id] != nil:
		if !onePatchKind(body) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			reply(map[string]any{"error": "RequestValidationError", "detail": "matches no update"})
			return
		}
		f.patches = append(f.patches, body)
		s := f.subs[id]
		if v, ok := body["seats"]; ok {
			s["seats"] = v
		}
		if v, ok := body["cancel_at_period_end"]; ok {
			s["cancel_at_period_end"] = v
		}
		if body["trial_end"] == "now" {
			s["status"], s["trial_end"] = "active", time.Now().UTC()
		}
		reply(s)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePolar) set(id string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs[id] == nil {
		f.subs[id] = map[string]any{"id": id, "product_id": "prod1", "current_period_end": time.Now().Add(30 * 24 * time.Hour).UTC()}
	}
	for k, v := range fields {
		f.subs[id][k] = v
	}
}

const testWebhookSecret = "whsec_dGVzdHNlY3JldHRlc3RzZWNyZXQ=" // gitleaks:allow (base64 of "testsecrettestsecret")

// deliver posts a signed webhook the way Polar does.
func deliver(t *testing.T, srv *httptest.Server, id, typ, subID string, sign bool) int {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"type":%q,"timestamp":"2026-10-14T00:00:00Z","data":{"id":%q}}`, typ, subID))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(testWebhookSecret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "." + string(body)))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !sign {
		sig = "v1,bm9wZQ=="
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+billing.WebhookPath, bytes.NewReader(body))
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", sig)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// Billing: no plan, no new nodes; checkout with the free month once;
// webhooks fetch the subscription; seats follow nodes; a second node ends
// the free month; an unpaid org gets 7 days, then its nodes are read-only.
func TestBilling(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	polar := &fakePolar{subs: map[string]map[string]any{}, invoices: map[string]bool{}}
	polarSrv := httptest.NewServer(polar)
	defer polarSrv.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	bill := &billing.Service{
		DB: db, Polar: &billing.Polar{Token: "polar_oat_test", API: polarSrv.URL}, ProductID: "prod1",
		WebhookSecret: testWebhookSecret, AppURL: appOrigin, Mailer: mail, Now: clock,
		Log: slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Billing: bill}
	node := &fakeNode{t: t, panelKey: pub}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: orgSvc, Billing: bill, AppOrigin: appOrigin,
		Staff:    &staff.Service{DB: db, Auth: authSvc, BillingLive: bill.Enabled},
		Commands: &commands.Service{Auth: authSvc, Sender: node, PanelKey: key, Billing: bill},
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

	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
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
	exec("INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID)

	b := get()
	if !b.GetEnabled() || b.GetStatus() != "" || !b.GetTrialAvailable() || b.GetNodePriceCents() != 1200 {
		t.Fatalf("a new org: %v", b)
	}
	// No plan: no join tokens.
	if _, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a join token without a plan: %v", err)
	}
	if _, err := bob.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member starting checkout: %v", err)
	}
	co, err := alice.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org})
	if err != nil || co.GetUrl() != "https://polar.test/checkout/co1" {
		t.Fatalf("checkout: %v, %v", co, err)
	}
	c := polar.checkouts[0]
	if c["external_customer_id"] != org || c["seats"] != float64(1) || c["allow_trial"] != true || c["trial_interval"] != "month" ||
		c["customer_email"] != "alice@example.com" || !strings.HasSuffix(c["success_url"].(string), "/orgs/"+org+"/settings/billing?checkout=done") {
		t.Errorf("checkout body: %v", c)
	}

	// Polar makes the subscription and says so.
	polar.set("sub1", map[string]any{
		"status": "trialing", "seats": 1, "trial_start": now.UTC(), "trial_end": now.Add(30 * 24 * time.Hour).UTC(),
		"customer": map[string]any{"id": "cus1", "external_id": org},
	})
	if code := deliver(t, srv, "msg1", "subscription.created", "sub1", false); code != http.StatusUnauthorized {
		t.Errorf("an unsigned webhook: %d", code)
	}
	if code := deliver(t, srv, "msg1", "subscription.created", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	if code := deliver(t, srv, "msg1", "subscription.created", "sub1", true); code != http.StatusAccepted {
		t.Errorf("a repeated webhook: %d", code)
	}
	if b := get(); b.GetStatus() != "trialing" || b.GetTrialAvailable() || b.GetTrialEnd() == nil {
		t.Fatalf("after the webhook: %v", b)
	}
	if _, err := alice.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a second plan: %v", err)
	}
	if _, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box"}); err != nil {
		t.Fatalf("a join token on the free month: %v", err)
	}

	// One node: the free month. A second ends it, and seats follow.
	newNode := func() string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	n1 := newNode()
	sweep()
	if len(polar.patches) != 0 {
		t.Errorf("one node on the free month changed the plan: %v", polar.patches)
	}
	newNode()
	sweep()
	if b := get(); b.GetStatus() != "active" || b.GetSeats() != 2 || b.GetNodes() != 2 {
		t.Errorf("after a second node: %v (patches %v)", b, polar.patches)
	}
	// No nodes: the plan ends with the period; one back: it goes on.
	exec("UPDATE nodes SET deleted_at = now() WHERE org_id = $1", org)
	sweep()
	if b := get(); !b.GetCancelAtPeriodEnd() {
		t.Errorf("no nodes left: %v", b)
	}
	exec("UPDATE nodes SET deleted_at = NULL WHERE id = $1", n1)
	sweep()
	if b := get(); b.GetCancelAtPeriodEnd() || b.GetSeats() != 1 {
		t.Errorf("a node back: %v", b)
	}

	// A payment fails: 7 days from then, the nodes are read-only.
	polar.set("sub1", map[string]any{"status": "past_due", "past_due_at": now.Add(-8 * 24 * time.Hour).UTC()})
	if code := deliver(t, srv, "msg2", "subscription.past_due", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	sweep()
	if b := get(); !b.GetReadOnly() || b.GetReadOnlyAt() == nil {
		t.Fatalf("unpaid for 8 days: %v", b)
	}
	mail.mu.Lock()
	var sent []string
	for _, m := range mail.mail {
		if strings.HasPrefix(m, "alice@example.com\n") && strings.Contains(m, "Settings, Billing") {
			sent = append(sent, m)
		}
	}
	mail.mu.Unlock()
	if len(sent) != 2 || !strings.Contains(sent[1], "read-only") {
		t.Errorf("emails: %q (all: %q)", sent, mail.mail)
	}
	sweep() // not again
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: n1, Action: "server.start", ServerId: "s1"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a change while read-only: %v", err)
	}
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: n1, Action: "node.health"}); err != nil {
		t.Errorf("a look while read-only: %v", err)
	}
	if _, err := alice.orgs.CreateJoinToken(ctx, &panelv1.CreateJoinTokenRequest{OrgId: org, Name: "box"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a join token while read-only: %v", err)
	}
	if p, err := alice.bill.OpenBillingPortal(ctx, &panelv1.OpenBillingPortalRequest{OrgId: org}); err != nil || p.GetUrl() == "" {
		t.Errorf("the portal: %v, %v", p, err)
	}
	// Paid: back to normal.
	polar.set("sub1", map[string]any{"status": "active", "past_due_at": nil})
	if code := deliver(t, srv, "msg3", "subscription.active", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	sweep()
	if b := get(); b.GetReadOnly() || b.GetReadOnlyAt() != nil {
		t.Errorf("paid: %v", b)
	}
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: n1, Action: "server.start", ServerId: "s1"}); err != nil {
		t.Errorf("a change once paid: %v", err)
	}

	// The billing page's own portal: cards, charges, invoices.
	polar.mu.Lock()
	card := func(id, last4 string) map[string]any {
		return map[string]any{"id": id, "type": "card", "method_metadata": map[string]any{"brand": "visa", "last4": last4, "exp_month": 4, "exp_year": 2030}}
	}
	pm1, pm2 := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	polar.methods, polar.defaultM = []map[string]any{card(pm1, "4242"), card(pm2, "1881")}, pm1
	ord := "33333333-3333-4333-8333-333333333333"
	polar.orders = []map[string]any{{
		"id": ord, "created_at": now.UTC(), "status": "paid", "paid": true, "total_amount": 1200,
		"currency": "usd", "billing_reason": "subscription_cycle", "invoice_number": "RAPTOR-0001",
	}}
	polar.mu.Unlock()
	if _, err := bob.bill.GetBillingDetails(ctx, &panelv1.GetBillingDetailsRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member reading cards: %v", err)
	}
	d, err := alice.bill.GetBillingDetails(ctx, &panelv1.GetBillingDetailsRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.GetCards()) != 2 || !d.GetCards()[0].GetDefault() || d.GetCards()[1].GetDefault() || d.GetCards()[0].GetLast4() != "4242" ||
		len(d.GetCharges()) != 1 || d.GetCharges()[0].GetTotalCents() != 1200 || d.GetCharges()[0].GetInvoiceNumber() != "RAPTOR-0001" {
		t.Errorf("details: %v", d)
	}
	if _, err := alice.bill.RemoveCard(ctx, &panelv1.RemoveCardRequest{OrgId: org, CardId: pm1}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("removing the card the plan uses: %v", err)
	}
	if _, err := alice.bill.RemoveCard(ctx, &panelv1.RemoveCardRequest{OrgId: org, CardId: "../orders"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a card ID that's a path: %v", err)
	}
	if _, err := alice.bill.SetDefaultCard(ctx, &panelv1.SetDefaultCardRequest{OrgId: org, CardId: pm2}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.bill.RemoveCard(ctx, &panelv1.RemoveCardRequest{OrgId: org, CardId: pm1}); err != nil {
		t.Fatal(err)
	}
	if d, _ := alice.bill.GetBillingDetails(ctx, &panelv1.GetBillingDetailsRequest{OrgId: org}); len(d.GetCards()) != 1 || !d.GetCards()[0].GetDefault() {
		t.Errorf("after switching cards: %v", d)
	}
	add, err := alice.bill.StartAddCard(ctx, &panelv1.StartAddCardRequest{OrgId: org})
	if err != nil || !strings.HasPrefix(add.GetSessionToken(), "polar_cst_") || add.GetPolarOrigin() != "https://polar.sh" {
		t.Errorf("adding a card: %v, %v", add, err)
	}
	// An invoice is made the first time it's asked for.
	if _, err := alice.bill.GetInvoice(ctx, &panelv1.GetInvoiceRequest{OrgId: org, ChargeId: ord}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("an invoice not made yet: %v", err)
	}
	if inv, err := alice.bill.GetInvoice(ctx, &panelv1.GetInvoiceRequest{OrgId: org, ChargeId: ord}); err != nil || !strings.HasSuffix(inv.GetUrl(), ord+".pdf") {
		t.Errorf("the invoice: %v, %v", inv, err)
	}
	if _, err := alice.bill.GetInvoice(ctx, &panelv1.GetInvoiceRequest{OrgId: org, ChargeId: pm1}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an invoice for no charge: %v", err)
	}
	// The owner cancels here and takes it back; the sweep leaves both alone.
	if _, err := alice.bill.CancelPlan(ctx, &panelv1.CancelPlanRequest{OrgId: org, Reason: "bored"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown reason: %v", err)
	}
	if _, err := alice.bill.CancelPlan(ctx, &panelv1.CancelPlanRequest{OrgId: org, Reason: "too_expensive", Comment: "  pricey "}); err != nil {
		t.Fatal(err)
	}
	polar.mu.Lock()
	last := polar.patches[len(polar.patches)-1]
	polar.mu.Unlock()
	if last["cancel_at_period_end"] != true || last["customer_cancellation_reason"] != "too_expensive" || last["customer_cancellation_comment"] != "pricey" {
		t.Errorf("the cancellation: %v", last)
	}
	sweep()
	if b := get(); !b.GetCancelAtPeriodEnd() {
		t.Errorf("the sweep undid the owner's cancellation: %v", b)
	}
	if _, err := alice.bill.CancelPlan(ctx, &panelv1.CancelPlanRequest{OrgId: org, Resume: true}); err != nil {
		t.Fatal(err)
	}
	if b := get(); b.GetCancelAtPeriodEnd() {
		t.Errorf("resumed: %v", b)
	}

	// Staff see it: recurring revenue from the seats on active plans, and
	// payments by month from the ledger.
	admin, adminID := signUp("admin@raptor.example")
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')`, adminID, []byte(adminID))
	exec(`UPDATE sessions SET passkey_at = now() WHERE user_id = $1`, adminID)
	exec(`INSERT INTO billing_ledger (org_id, kind, amount_cents, currency) VALUES ($1, 'order.paid', 1200, 'usd'), ($1, 'order.refunded', 200, 'usd')`, org)
	ins, err := admin.staff.GetBillingInsights(ctx, &panelv1.GetBillingInsightsRequest{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !ins.GetBillingLive() || ins.GetMrrCents() != 1200 || ins.GetSeatsBilled() != 1 || len(ins.GetRevenueByMonth()) != 1 ||
		ins.GetRevenueByMonth()[0].GetPaidCents() != 1200 || ins.GetRevenueByMonth()[0].GetRefundedCents() != 200 {
		t.Errorf("billing insights: %v", ins)
	}
	ov, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r := ov.GetRevenue(); !r.GetBillingLive() || r.GetMrrCents() != 1200 || r.GetPayingOrgs() != 1 || r.GetRecentPaidCents() != 1000 {
		t.Errorf("overview revenue: %v", r)
	}
	// The owner cancels in Polar's portal: the sweep leaves it alone.
	polar.mu.Lock()
	before := len(polar.patches)
	polar.mu.Unlock()
	polar.set("sub1", map[string]any{"cancel_at_period_end": true})
	if code := deliver(t, srv, "msg4", "subscription.canceled", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	sweep()
	polar.mu.Lock()
	after := len(polar.patches)
	polar.mu.Unlock()
	if after != before {
		t.Errorf("the sweep undid the owner's cancellation: %v", polar.patches[before:])
	}
	// It ends: a new plan has no free month.
	polar.set("sub1", map[string]any{"status": "canceled", "ended_at": now.UTC()})
	if code := deliver(t, srv, "msg5", "subscription.revoked", "sub1", true); code != http.StatusAccepted {
		t.Fatalf("webhook: %d", code)
	}
	if b := get(); b.GetTrialAvailable() || b.GetStatus() != "canceled" {
		t.Errorf("after the plan ended: %v", b)
	}
	if _, err := alice.bill.StartCheckout(ctx, &panelv1.StartCheckoutRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	if c := polar.checkouts[len(polar.checkouts)-1]; c["allow_trial"] != false || c["trial_interval"] != nil {
		t.Errorf("a second checkout offered the free month: %v", c)
	}
	var ledger int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM billing_ledger WHERE org_id = $1", org).Scan(&ledger)
	if ledger < 5 {
		t.Errorf("ledger entries: %d", ledger)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
