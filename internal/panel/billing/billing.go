// Package billing charges orgs for their nodes through Polar
// (docs/PANEL.md#billing-polar): one seat-based subscription per org,
// whose seats follow its linked nodes. Polar holds the truth; the Panel
// keeps what it last fetched (billing_accounts) and a ledger.
//
// Webhooks only say which subscription changed: the Panel fetches it, so
// deliveries out of order can't leave an old state behind. A sweep keeps
// seats equal to nodes, and decides who's unpaid: an org with nodes and no
// subscription in force gets 7 days, then its nodes are read-only (no
// changes; servers keep running) until it pays.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Pricing and timing (DECISIONS #14, #15).
const (
	// NodePriceCents is a node's monthly price.
	NodePriceCents = 1200
	// Grace is how long an unpaid org keeps full use.
	Grace = 7 * 24 * time.Hour
	// refreshEvery is how often the sweep fetches a subscription even
	// without a webhook, in case one was lost.
	refreshEvery = time.Hour
)

// Errors.
var (
	ErrReadOnly = connect.NewError(connect.CodeFailedPrecondition,
		errors.New("this org's nodes are read-only until its bill is paid: an owner can fix it in Settings, Billing. Servers keep running"))
	ErrNoPlan = connect.NewError(connect.CodeFailedPrecondition,
		errors.New("start a plan first, in Settings, Billing: your first node is free for a month"))
)

// Mailer sends email (auth.Mailer).
type Mailer interface {
	Send(ctx context.Context, to, subject, text string) error
}

// Service is billing. A nil Service, or one without Polar, is billing off:
// everything is allowed and nothing is charged.
type Service struct {
	DB    *pgxpool.Pool
	Polar *Polar
	// ProductID is the seat-based product ($12 a seat a month).
	ProductID     string
	WebhookSecret string
	// AppURL is the web app's origin, for the links back from Polar.
	AppURL string
	Mailer Mailer
	Log    *slog.Logger
	Now    func() time.Time
}

// Enabled reports whether billing is on.
func (s *Service) Enabled() bool {
	return s != nil && s.Polar != nil && s.ProductID != ""
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func pgID(org uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: org, Valid: true} }

func ts(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// State is an org's billing, for its pages.
type State struct {
	Enabled bool
	Account store.BillingAccount // zero if it never had one
	Nodes   int
	// ReadOnlyAt is when an unpaid org's nodes go (or went) read-only;
	// zero if it's paid.
	ReadOnlyAt     time.Time
	ReadOnly       bool
	TrialAvailable bool
}

func (s *Service) account(ctx context.Context, q *store.Queries, org pgtype.UUID) (store.BillingAccount, error) {
	a, err := q.BillingAccount(ctx, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.BillingAccount{OrgID: org}, nil
	}
	return a, err
}

// State reads an org's billing.
func (s *Service) State(ctx context.Context, org uuid.UUID) (State, error) {
	st := State{Enabled: s.Enabled()}
	if !st.Enabled {
		return st, nil
	}
	q := store.New(s.DB)
	a, err := s.account(ctx, q, pgID(org))
	if err != nil {
		return st, err
	}
	n, err := q.OrgNodeCount(ctx, pgID(org))
	if err != nil {
		return st, err
	}
	st.Account, st.Nodes = a, int(n)
	if a.UnpaidSince.Valid {
		st.ReadOnlyAt = a.UnpaidSince.Time.Add(Grace)
		st.ReadOnly = !s.now().Before(st.ReadOnlyAt)
	}
	st.TrialAvailable = !a.TrialUsed && st.Nodes <= 1
	return st, nil
}

// CheckWrite refuses changes to an org's nodes while it's read-only.
func (s *Service) CheckWrite(ctx context.Context, org uuid.UUID) error {
	if !s.Enabled() {
		return nil
	}
	st, err := s.State(ctx, org)
	if err != nil {
		return err
	}
	if st.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// CheckAddNode says whether an org may link another node: it needs a plan
// in force and not to be read-only. (A second node during the free month
// ends it: the sweep does that.)
func (s *Service) CheckAddNode(ctx context.Context, org uuid.UUID) error {
	if !s.Enabled() {
		return nil
	}
	st, err := s.State(ctx, org)
	if err != nil {
		return err
	}
	switch {
	case st.ReadOnly:
		return ErrReadOnly
	case !Live(st.Account.Status):
		return ErrNoPlan
	}
	return nil
}

func (s *Service) billingURL(org uuid.UUID) string {
	return strings.TrimRight(s.AppURL, "/") + "/orgs/" + org.String() + "/settings/billing"
}

// Checkout starts a plan: Polar's checkout for one seat per node (at least
// one), with the free month if the org hasn't had it. It returns the URL
// to send the owner to.
func (s *Service) Checkout(ctx context.Context, org uuid.UUID, email string) (string, error) {
	if !s.Enabled() {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("billing isn't on"))
	}
	st, err := s.State(ctx, org)
	if err != nil {
		return "", err
	}
	if Live(st.Account.Status) {
		return "", connect.NewError(connect.CodeAlreadyExists, errors.New("this org already has a plan: manage it from the billing portal"))
	}
	back := s.billingURL(org)
	return s.Polar.CreateCheckout(ctx, Checkout{
		ProductID: s.ProductID, OrgID: org.String(), Email: email, Seats: max(st.Nodes, 1),
		Trial: st.TrialAvailable, SuccessURL: back + "?checkout=done", ReturnURL: back,
	})
}

// Portal is a link to Polar's customer portal for the org.
func (s *Service) Portal(ctx context.Context, org uuid.UUID) (string, error) {
	if !s.Enabled() {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("billing isn't on"))
	}
	return s.Polar.PortalURL(ctx, org.String(), s.billingURL(org))
}

// EndTrial ends the free month now, so the org can add nodes (they're
// charged from now on).
func (s *Service) EndTrial(ctx context.Context, org uuid.UUID) error {
	if !s.Enabled() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("billing isn't on"))
	}
	st, err := s.State(ctx, org)
	if err != nil {
		return err
	}
	if st.Account.Status != "trialing" {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the plan isn't in its free month"))
	}
	sub, err := s.Polar.UpdateSubscription(ctx, st.Account.SubscriptionID, map[string]any{"trial_end": "now"})
	if err != nil {
		return err
	}
	return s.apply(ctx, sub, "trial.ended_early")
}

// apply saves a subscription Polar sent or returned, if it's the org's
// current one, and notes a change of state in the ledger.
func (s *Service) apply(ctx context.Context, sub *Subscription, why string) error {
	org, err := uuid.Parse(sub.Org())
	if err != nil {
		s.log().Warn("a subscription without an org", "subscription", sub.ID)
		return nil //nolint:nilerr // not one of Raptor's: nothing to save
	}
	q := store.New(s.DB)
	cur, err := s.account(ctx, q, pgID(org))
	if err != nil {
		return err
	}
	// An older subscription ending doesn't replace the one in force.
	if cur.SubscriptionID != "" && cur.SubscriptionID != sub.ID && Live(cur.Status) && !sub.Live() {
		return nil
	}
	seats := 0
	if sub.Seats != nil {
		seats = *sub.Seats
	}
	err = q.SaveSubscription(ctx, store.SaveSubscriptionParams{
		OrgID: pgID(org), SubscriptionID: sub.ID, Status: sub.Status, Seats: int32(seats), //nolint:gosec // seats are at most 10000
		CurrentPeriodEnd: ts(sub.CurrentPeriodEnd), TrialEnd: ts(sub.TrialEnd),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd, PastDueAt: ts(sub.PastDueAt), TrialUsed: sub.TrialStart != nil,
	})
	if err != nil {
		return err
	}
	if cur.SubscriptionID != sub.ID || cur.Status != sub.Status || why != "" {
		kind := "subscription." + sub.Status
		if why != "" {
			kind = why
		}
		s.ledger(ctx, q, pgID(org), kind, seats, 0, "", sub.ID, map[string]any{"status": sub.Status})
	}
	return nil
}

func (s *Service) ledger(ctx context.Context, q *store.Queries, org pgtype.UUID, kind string, qty int, cents int64, currency, ref string, data map[string]any) {
	b, _ := json.Marshal(data)
	if err := q.AddLedger(ctx, store.AddLedgerParams{
		OrgID: org, Kind: kind, Quantity: int32(qty), AmountCents: cents, Currency: currency, PolarRef: ref, Data: b, //nolint:gosec // small
	}); err != nil {
		s.log().Error("writing the billing ledger failed", "org", uuid.UUID(org.Bytes), "kind", kind, "err", err)
	}
}

// ServeHTTP receives Polar's webhooks.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() || s.WebhookSecret == "" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := VerifyWebhook(s.WebhookSecret, r.Header, body, s.now()); err != nil {
		s.log().Warn("a billing webhook failed its signature check", "remote", r.RemoteAddr)
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	if err := s.handle(r.Context(), r.Header.Get("webhook-id"), body); err != nil {
		s.log().Error("handling a billing webhook failed", "id", r.Header.Get("webhook-id"), "err", err)
		http.Error(w, "try again", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

type event struct {
	Type string `json:"type"`
	Data struct {
		ID             string `json:"id"`
		SubscriptionID string `json:"subscription_id"`
		TotalAmount    int64  `json:"total_amount"`
		Currency       string `json:"currency"`
		BillingReason  string `json:"billing_reason"`
		Customer       struct {
			ExternalID string `json:"external_id"`
		} `json:"customer"`
	} `json:"data"`
}

func (s *Service) handle(ctx context.Context, id string, body []byte) (err error) {
	var ev event
	if err := json.Unmarshal(body, &ev); err != nil {
		return fmt.Errorf("webhook body: %w", err)
	}
	q := store.New(s.DB)
	if _, err := q.SeenWebhook(ctx, store.SeenWebhookParams{ID: id, Type: ev.Type}); errors.Is(err, pgx.ErrNoRows) {
		return nil // handled before
	} else if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = q.ForgetWebhook(context.WithoutCancel(ctx), id)
		}
	}()
	switch {
	case strings.HasPrefix(ev.Type, "subscription."):
		sub, err := s.Polar.Subscription(ctx, ev.Data.ID)
		if err != nil {
			return err
		}
		if err := s.apply(ctx, sub, ""); err != nil {
			return err
		}
		if org, err := uuid.Parse(sub.Org()); err == nil {
			go s.sweepOne(context.WithoutCancel(ctx), org)
		}
	case ev.Type == "order.paid" || ev.Type == "order.refunded":
		org, err := uuid.Parse(ev.Data.Customer.ExternalID)
		if err != nil {
			return nil //nolint:nilerr // not one of ours
		}
		s.ledger(ctx, q, pgID(org), ev.Type, 0, ev.Data.TotalAmount, ev.Data.Currency, ev.Data.ID,
			map[string]any{"subscription": ev.Data.SubscriptionID, "reason": ev.Data.BillingReason})
	}
	return nil
}

// WebhookPath is where Polar sends webhooks, on the API's origin.
const WebhookPath = "/billing/polar/webhook"

// lockID is the advisory lock a sweep holds: one Panel instance at a time.
const lockID = 0x7261_7074_6269_6c6c // "raptbill"

// Run sweeps every interval until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("billing sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep looks at every org with nodes or a subscription.
func (s *Service) Sweep(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()
	rows, err := store.New(s.DB).BillingOrgs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		if err := s.sweep(ctx, r); err != nil {
			errs = append(errs, fmt.Errorf("org %s: %w", uuid.UUID(r.OrgID.Bytes), err))
		}
	}
	return errors.Join(errs...)
}

// sweepOne sweeps one org now (after a webhook, or a node linked or
// removed), unless a full sweep is running.
func (s *Service) sweepOne(ctx context.Context, org uuid.UUID) {
	if !s.Enabled() {
		return
	}
	rows, err := store.New(s.DB).BillingOrgs(ctx)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.OrgID.Bytes == org {
			if err := s.sweep(ctx, r); err != nil {
				s.log().Warn("billing sweep failed", "org", org, "err", err)
			}
		}
	}
}

// NodesChanged is called when an org links or removes a node.
func (s *Service) NodesChanged(org uuid.UUID) {
	if s.Enabled() {
		go s.sweepOne(context.Background(), org)
	}
}

func (s *Service) sweep(ctx context.Context, r store.BillingOrgsRow) error {
	q := store.New(s.DB)
	org := uuid.UUID(r.OrgID.Bytes)
	now := s.now()
	status, nodes := r.Status, int(r.Nodes)

	// Fetch now and then, in case a webhook was lost.
	if r.SubscriptionID != "" && (!r.FetchedAt.Valid || now.Sub(r.FetchedAt.Time) > refreshEvery) {
		sub, err := s.Polar.Subscription(ctx, r.SubscriptionID)
		if err != nil {
			return err
		}
		if err := s.apply(ctx, sub, ""); err != nil {
			return err
		}
		status, r.CancelAtPeriodEnd, r.PastDueAt = sub.Status, sub.CancelAtPeriodEnd, ts(sub.PastDueAt)
		if sub.Seats != nil {
			r.Seats = int32(*sub.Seats) //nolint:gosec // at most 10000
		}
	}

	// The free month covers one node: a second ends it.
	if status == "trialing" && nodes > 1 {
		sub, err := s.Polar.UpdateSubscription(ctx, r.SubscriptionID, map[string]any{"trial_end": "now"})
		if err != nil {
			return err
		}
		if err := s.apply(ctx, sub, "trial.ended_second_node"); err != nil {
			return err
		}
		status = sub.Status
	}

	// Seats follow nodes. Removing the last one ends the plan at the
	// period's end (a new plan has none yet: that's fine), and linking one
	// again takes that back; an owner's own cancellation is left alone.
	autoCanceled := r.AutoCanceled
	if Live(status) {
		change := map[string]any{}
		switch {
		case nodes == 0 && r.LastNodes > 0 && !r.CancelAtPeriodEnd:
			change["cancel_at_period_end"], autoCanceled = true, true
		case nodes > 0 && r.CancelAtPeriodEnd && r.AutoCanceled:
			change["cancel_at_period_end"], autoCanceled = false, false
		}
		if nodes > 0 && int(r.Seats) != nodes {
			change["seats"], change["proration_behavior"] = nodes, "prorate"
		}
		if len(change) > 0 {
			sub, err := s.Polar.UpdateSubscription(ctx, r.SubscriptionID, change)
			if err != nil {
				return err
			}
			if err := s.apply(ctx, sub, "seats"); err != nil {
				return err
			}
			s.log().Info("billing: subscription updated", "org", org, "nodes", nodes, "change", change)
		}
	}

	if int(r.LastNodes) != nodes || autoCanceled != r.AutoCanceled {
		if err := q.SetSweptNodes(ctx, store.SetSweptNodesParams{OrgID: r.OrgID, Nodes: int32(nodes), AutoCanceled: autoCanceled}); err != nil { //nolint:gosec // small
			return err
		}
	}

	// Unpaid: nodes and no plan in force (a failed payment counts from
	// when it failed).
	unpaid := nodes > 0 && status != "trialing" && status != "active"
	since := r.UnpaidSince
	notified := r.UnpaidNotified
	switch {
	case !unpaid && since.Valid:
		since, notified = pgtype.Timestamptz{}, []string{}
		s.ledger(ctx, q, r.OrgID, "unpaid.cleared", nodes, 0, "", r.SubscriptionID, nil)
	case unpaid && !since.Valid:
		start := now
		if status == "past_due" && r.PastDueAt.Valid {
			start = r.PastDueAt.Time
		}
		since, notified = pgtype.Timestamptz{Time: start, Valid: true}, []string{}
		s.ledger(ctx, q, r.OrgID, "unpaid", nodes, 0, "", r.SubscriptionID, map[string]any{"status": status})
	}
	if since.Valid {
		readOnlyAt := since.Time.Add(Grace)
		if !slices.Contains(notified, "reminder") {
			s.email(ctx, q, r.OrgID, "Raptor: your nodes need a plan",
				fmt.Sprintf("Your org %%s has linked nodes and no plan that's paid up.\n\nAdd or fix one by %s in Settings, Billing:\n%s\n\nAfter that, your nodes are read-only in the Panel until it's paid. Your game servers keep running either way.\n",
					readOnlyAt.UTC().Format("January 2, 2006"), s.billingURL(org)))
			notified = append(notified, "reminder")
		}
		if !now.Before(readOnlyAt) && !slices.Contains(notified, "read_only") {
			s.email(ctx, q, r.OrgID, "Raptor: your nodes are read-only",
				fmt.Sprintf("Your org %%s's nodes are now read-only in the Panel: nothing can be changed until its bill is paid. Your game servers keep running.\n\nFix it in Settings, Billing:\n%s\n\nOr remove nodes you no longer use.\n", s.billingURL(org)))
			notified = append(notified, "read_only")
			s.ledger(ctx, q, r.OrgID, "read_only", nodes, 0, "", r.SubscriptionID, nil)
		}
	}
	if since != r.UnpaidSince || !slices.Equal(notified, r.UnpaidNotified) {
		return q.SetUnpaid(ctx, store.SetUnpaidParams{OrgID: r.OrgID, UnpaidSince: since, UnpaidNotified: notified})
	}
	return nil
}

// email writes to the org's owners; text's %s is the org's name.
func (s *Service) email(ctx context.Context, q *store.Queries, org pgtype.UUID, subject, text string) {
	if s.Mailer == nil {
		return
	}
	name, err := q.OrgName(ctx, org)
	if err != nil {
		s.log().Warn("a billing email failed", "org", uuid.UUID(org.Bytes), "err", err)
		return
	}
	to, err := q.OrgOwnerEmails(ctx, org)
	if err != nil {
		s.log().Warn("a billing email failed", "org", uuid.UUID(org.Bytes), "err", err)
		return
	}
	for _, addr := range to {
		if err := s.Mailer.Send(ctx, addr, subject, fmt.Sprintf(text, name)); err != nil {
			s.log().Warn("a billing email failed", "org", uuid.UUID(org.Bytes), "err", err)
		}
	}
}
