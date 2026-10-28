package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// Raptor Backup Storage, charged (DECISIONS #335): nothing until the meter
// is set; then one event a day per org over what's included, only for
// plans in force that have the storage price and no storage waiver; a day
// is sent once, and a failed send is sent again.
func TestBackupStorageBilling(t *testing.T) {
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
		Staff: &staff.Service{DB: db, Auth: authSvc, BillingLive: bill.Enabled, StorageBillingLive: bill.StorageBilling},
	}))
	defer srv.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
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
	const gib = int64(1) << 30
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	// An org with a node and today's measured use.
	newOrg := func(name string, used int64) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ($1) RETURNING id", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex'))`, id)
		exec("INSERT INTO backup_storage_usage (org_id, day, bytes) VALUES ($1, $2, $3)", id, today, used)
		return id
	}
	msgs := 0
	plan := func(sub, org string, priced bool) {
		t.Helper()
		prices := []any{map[string]any{"amount_type": "seat_based"}}
		if priced {
			prices = append(prices, map[string]any{"amount_type": "metered_unit", "meter_id": "meter1"})
		}
		polar.set(sub, map[string]any{
			"status": "active", "seats": 1, "customer": map[string]any{"id": "c-" + sub, "external_id": org},
			"prices": prices, "meters": []any{map[string]any{"meter_id": "meter1", "amount": 18, "consumed_units": 1.5}},
		})
		msgs++
		if code := deliver(t, srv, fmt.Sprintf("msg-%d", msgs), "subscription.updated", sub, true); code != http.StatusAccepted {
			t.Fatalf("webhook: %d", code)
		}
	}
	billed := func(org string) (skipped string, sent bool, ok bool) {
		err := db.QueryRow(ctx, "SELECT skipped, sent_at IS NOT NULL FROM backup_storage_billing WHERE org_id = $1 AND day = $2", org, today).Scan(&skipped, &sent)
		return skipped, sent, err == nil
	}

	alice, _ := signUp("alice@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	acme := created.GetOrg().GetId()
	exec(`INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex'))`, acme)
	exec("INSERT INTO backup_storage_usage (org_id, day, bytes) VALUES ($1, $2, $3), ($1, $4, $5)", acme, today, 25*gib, today.AddDate(0, 0, -3), 90*gib)
	noPlan := newOrg("No plan", 50*gib)
	notPriced := newOrg("Old price", 50*gib)
	waived := newOrg("Partner", 50*gib)
	small := newOrg("Small", 9*gib)
	exec("INSERT INTO billing_waivers (org_id, storage, reason) VALUES ($1, true, 'partner')", waived)

	// Off until the meter is set: nothing sent, nothing recorded.
	plan("sub-acme", acme, true)
	if err := bill.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if b, err := alice.bill.GetBilling(ctx, &panelv1.GetBillingRequest{OrgId: acme}); err != nil || b.GetStorageBilling() || b.GetStoragePriced() {
		t.Errorf("billing without the meter: %v, %v", b, err)
	}
	var rows int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM backup_storage_billing").Scan(&rows)
	if rows != 0 || len(polar.events) != 0 {
		t.Fatalf("with the meter unset: %d rows, events %v", rows, polar.events)
	}

	bill.StorageMeterID = "meter1"
	plan("sub-acme", acme, true)
	plan("sub-old", notPriced, false)
	plan("sub-partner", waived, true)
	plan("sub-small", small, true)

	// Polar down: nothing is recorded as sent, and it's sent next time.
	polar.mu.Lock()
	polar.ingestDown = true
	polar.mu.Unlock()
	if err := bill.Sweep(ctx); err == nil {
		t.Error("a sweep with Polar's events down didn't fail")
	}
	if _, _, ok := billed(acme); ok {
		t.Error("recorded while Polar was down")
	}
	polar.mu.Lock()
	polar.ingestDown = false
	polar.mu.Unlock()
	if err := bill.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[string]string{noPlan: "no_plan", notPriced: "not_on_plan", waived: "waived", small: "included", acme: ""} {
		if skipped, sent, ok := billed(org); !ok || skipped != want || sent != (want == "") {
			t.Errorf("org %s: skipped %q, sent %v, recorded %v; want %q", org, skipped, sent, ok, want)
		}
	}
	polar.mu.Lock()
	events := append([]map[string]any(nil), polar.events...)
	polar.mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("events: %v", events)
	}
	ev := events[0]
	meta, _ := ev["metadata"].(map[string]any)
	want := billing.GBMonths(15*gib, today)
	if ev["name"] != "backup_storage" || ev["external_customer_id"] != acme ||
		ev["external_id"] != "backup_storage:"+acme+":"+today.Format(time.DateOnly) || meta["gb_months"] != want ||
		meta["over_bytes"] != float64(15*gib) {
		t.Errorf("the event: %v (want %v GB-months)", ev, want)
	}
	// Once a day: not again, and a repeat is one Polar already has.
	if err := bill.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	exec("DELETE FROM backup_storage_billing WHERE org_id = $1", acme)
	if err := bill.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	polar.mu.Lock()
	n := len(polar.events)
	polar.mu.Unlock()
	if n != 1 {
		t.Errorf("events after more sweeps: %d", n)
	}

	// The owner's page: storage, what's included, and the charge so far.
	b, err := alice.bill.GetBilling(ctx, &panelv1.GetBillingRequest{OrgId: acme})
	if err != nil {
		t.Fatal(err)
	}
	if !b.GetStorageBilling() || !b.GetStoragePriced() || b.GetStorageUsedBytes() != 25*gib || b.GetStorageIncludedBytes() != 10*gib ||
		b.GetStoragePeriodCents() != 18 || b.GetStorageCentsPerTb() != 1200 || b.GetStorageMeasuredAt() == nil {
		t.Errorf("the billing page: %v", b)
	}

	// Staff: it's charged now.
	admin, adminID := signUp("admin@raptor.example")
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')`, adminID, []byte(adminID))
	exec(`UPDATE sessions SET passkey_at = now() WHERE user_id = $1`, adminID)
	ov, err := admin.staff.GetOverview(ctx, &panelv1.GetOverviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := admin.staff.GetBillingInsights(ctx, &panelv1.GetBillingInsightsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ov.GetRevenue().GetStorageBillingLive() || !ins.GetStorageBillingLive() {
		t.Errorf("staff: overview %v, insights %v", ov.GetRevenue(), ins.GetStorageBillingLive())
	}
}
