package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is Polar's production API; https://sandbox-api.polar.sh is
// its sandbox.
const DefaultAPI = "https://api.polar.sh"

// Polar calls Polar's API with an organization access token
// (checkouts:write, subscriptions:read, subscriptions:write,
// customer_sessions:write).
type Polar struct {
	Token string
	// API is the base URL (empty: DefaultAPI).
	API  string
	HTTP *http.Client
}

// ErrPolar is Polar not answering as it should.
var ErrPolar = errors.New("the payment provider isn't answering right now; try again in a minute")

func (p *Polar) do(ctx context.Context, method, path string, body, out any) error {
	api := p.API
	if api == "" {
		api = DefaultAPI
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := p.HTTP
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%w (%w)", ErrPolar, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w (%w)", ErrPolar, err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w (%s %s: %s: %.300s)", ErrPolar, method, path, resp.Status, data)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Subscription is what Raptor reads of Polar's.
type Subscription struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	Seats             *int       `json:"seats"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end"`
	TrialStart        *time.Time `json:"trial_start"`
	TrialEnd          *time.Time `json:"trial_end"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	EndedAt           *time.Time `json:"ended_at"`
	PastDueAt         *time.Time `json:"past_due_at"`
	ProductID         string     `json:"product_id"`
	Customer          struct {
		ID         string `json:"id"`
		ExternalID string `json:"external_id"`
	} `json:"customer"`
	Metadata map[string]any `json:"metadata"`
}

// Org is the org the subscription is for: the customer's external ID,
// which Raptor sets to the org's ID at checkout.
func (s *Subscription) Org() string {
	if s.Customer.ExternalID != "" {
		return s.Customer.ExternalID
	}
	if v, ok := s.Metadata["org_id"].(string); ok {
		return v
	}
	return ""
}

// Live reports whether the subscription is in force: trialing, paid up, or
// with a failed payment Polar is still retrying.
func (s *Subscription) Live() bool { return Live(s.Status) }

// Live reports whether a subscription status is in force.
func Live(status string) bool {
	return status == "trialing" || status == "active" || status == "past_due"
}

// Checkout starts a checkout for an org's subscription and returns its
// URL.
type Checkout struct {
	ProductID  string
	OrgID      string
	Email      string
	Seats      int
	Trial      bool // the first month free (once per org)
	SuccessURL string
	ReturnURL  string
}

// CreateCheckout implements the checkout.
func (p *Polar) CreateCheckout(ctx context.Context, c Checkout) (string, error) {
	body := map[string]any{
		"products":             []string{c.ProductID},
		"seats":                max(c.Seats, 1),
		"external_customer_id": c.OrgID,
		"metadata":             map[string]any{"org_id": c.OrgID},
		"success_url":          c.SuccessURL,
		"return_url":           c.ReturnURL,
		"allow_trial":          c.Trial,
	}
	if c.Email != "" {
		body["customer_email"] = c.Email
	}
	if c.Trial {
		body["trial_interval"], body["trial_interval_count"] = "month", 1
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := p.do(ctx, http.MethodPost, "/v1/checkouts/", body, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", ErrPolar
	}
	return out.URL, nil
}

// Subscription fetches one.
func (p *Polar) Subscription(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	if err := p.do(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSubscription changes a subscription: seats (prorated on the next
// invoice), cancelling at the period's end or not, or ending its trial.
func (p *Polar) UpdateSubscription(ctx context.Context, id string, change map[string]any) (*Subscription, error) {
	var s Subscription
	if err := p.do(ctx, http.MethodPatch, "/v1/subscriptions/"+url.PathEscape(id), change, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// PortalURL is a link to the customer portal for the org (cards, invoices,
// cancelling), good for a short while.
func (p *Polar) PortalURL(ctx context.Context, orgID, returnURL string) (string, error) {
	var out struct {
		URL string `json:"customer_portal_url"`
	}
	if err := p.do(ctx, http.MethodPost, "/v1/customer-sessions/", map[string]any{"external_customer_id": orgID, "return_url": returnURL}, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// Webhook signatures (Standard Webhooks, which Polar follows).

// ErrSignature is a webhook delivery that isn't from Polar, or is too old.
var ErrSignature = errors.New("bad webhook signature")

// webhookTolerance is how far a delivery's timestamp may be from now.
const webhookTolerance = 5 * time.Minute

// VerifyWebhook checks a delivery's webhook-signature header against the
// endpoint's secret (whsec_…): an HMAC-SHA256 of "id.timestamp.body". The
// key is the secret's base64 part, decoded (Standard Webhooks; secrets
// Polar made from 8 September 2026), or the whole secret's bytes (older
// ones); either is accepted, as Polar's SDKs do.
func VerifyWebhook(secret string, h http.Header, body []byte, now time.Time) error {
	id, ts, sigs := h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature")
	if id == "" || ts == "" || sigs == "" {
		return ErrSignature
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrSignature
	}
	if d := now.Sub(time.Unix(sec, 0)); d > webhookTolerance || d < -webhookTolerance {
		return ErrSignature
	}
	var keys [][]byte
	if k, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_")); err == nil {
		keys = append(keys, k)
	}
	keys = append(keys, []byte(secret))
	signed := []byte(id + "." + ts + "." + string(body))
	for _, key := range keys {
		mac := hmac.New(sha256.New, key)
		mac.Write(signed)
		want := mac.Sum(nil)
		for _, s := range strings.Fields(sigs) {
			v, sig, ok := strings.Cut(s, ",")
			if !ok || v != "v1" {
				continue
			}
			got, err := base64.StdEncoding.DecodeString(sig)
			if err == nil && hmac.Equal(got, want) {
				return nil
			}
		}
	}
	return ErrSignature
}
