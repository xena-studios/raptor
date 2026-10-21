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
	"slices"
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

// APIError is Polar answering with an error.
type APIError struct {
	Status int
	// Type is Polar's name for it, like PaymentMethodInUseByActiveSubscription.
	Type string
	msg  string
}

func (e *APIError) Error() string { return ErrPolar.Error() + " (" + e.msg + ")" }

// Unwrap makes it an ErrPolar.
func (e *APIError) Unwrap() error { return ErrPolar }

// polarError is the Polar error a call returned, if it is one, of one of
// these types.
func polarError(err error, types ...string) bool {
	var e *APIError
	return errors.As(err, &e) && slices.Contains(types, e.Type)
}

func (p *Polar) do(ctx context.Context, method, path string, body, out any) error {
	return p.call(ctx, p.Token, method, path, body, out)
}

// call is do with another token: a customer session's, for the customer
// portal API.
func (p *Polar) call(ctx context.Context, token, method, path string, body, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+token)
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
		e := &APIError{Status: resp.StatusCode, msg: fmt.Sprintf("%s %s: %s: %.300s", method, path, resp.Status, data)}
		var body struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &body) == nil {
			e.Type = body.Error
		}
		return e
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

// LiveStatuses are the subscription statuses in force.
var LiveStatuses = []string{"trialing", "active", "past_due"}

// Live reports whether a subscription status is in force.
func Live(status string) bool { return slices.Contains(LiveStatuses, status) }

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
	s, err := p.CustomerSession(ctx, orgID, returnURL)
	if err != nil {
		return "", err
	}
	return s.PortalURL, nil
}

// CustomerSession is a token for one customer's side of Polar: the
// customer portal API, and the embedded form for adding a card.
type CustomerSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	PortalURL string    `json:"customer_portal_url"`
}

// CustomerSession starts one for the org (an hour, as Polar makes them).
func (p *Polar) CustomerSession(ctx context.Context, orgID, returnURL string) (*CustomerSession, error) {
	body := map[string]any{"external_customer_id": orgID}
	if returnURL != "" {
		body["return_url"] = returnURL
	}
	var out CustomerSession
	if err := p.do(ctx, http.MethodPost, "/v1/customer-sessions/", body, &out); err != nil {
		return nil, err
	}
	if out.Token == "" {
		return nil, ErrPolar
	}
	return &out, nil
}

// Origin is where Polar's own pages are, for embedding them: the sandbox
// API's are on the sandbox.
func (p *Polar) Origin() string {
	if strings.Contains(p.API, "sandbox") {
		return "https://sandbox.polar.sh"
	}
	return "https://polar.sh"
}

// PaymentMethod is a card (or another way to pay) the customer saved.
type PaymentMethod struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Metadata struct {
		Brand    string `json:"brand"`
		Last4    string `json:"last4"`
		ExpMonth int    `json:"exp_month"`
		ExpYear  int    `json:"exp_year"`
		Wallet   string `json:"wallet"`
	} `json:"method_metadata"`
}

// Customer is what Raptor reads of the customer's own record.
type Customer struct {
	DefaultPaymentMethodID string `json:"default_payment_method_id"`
}

// Order is a charge: the first one, each renewal, and seat changes.
type Order struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	Status             string    `json:"status"`
	Paid               bool      `json:"paid"`
	TotalAmount        int64     `json:"total_amount"`
	RefundedAmount     int64     `json:"refunded_amount"`
	Currency           string    `json:"currency"`
	BillingReason      string    `json:"billing_reason"`
	Description        string    `json:"description"`
	InvoiceNumber      string    `json:"invoice_number"`
	IsInvoiceGenerated bool      `json:"is_invoice_generated"`
}

type page[T any] struct {
	Items []T `json:"items"`
}

// Customer fetches the session's customer.
func (p *Polar) Customer(ctx context.Context, token string) (*Customer, error) {
	var c Customer
	if err := p.call(ctx, token, http.MethodGet, "/v1/customer-portal/customers/me", nil, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// PaymentMethods lists the customer's saved ways to pay.
func (p *Polar) PaymentMethods(ctx context.Context, token string) ([]PaymentMethod, error) {
	var out page[PaymentMethod]
	if err := p.call(ctx, token, http.MethodGet, "/v1/customer-portal/customers/me/payment-methods?limit=100", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SetDefaultPaymentMethod makes one the method the subscription is
// charged to.
func (p *Polar) SetDefaultPaymentMethod(ctx context.Context, token, id string) error {
	return p.call(ctx, token, http.MethodPatch, "/v1/customer-portal/customers/me", map[string]any{"default_payment_method_id": id}, nil)
}

// DeletePaymentMethod removes one. Polar refuses the one a subscription
// still needs.
func (p *Polar) DeletePaymentMethod(ctx context.Context, token, id string) error {
	return p.call(ctx, token, http.MethodDelete, "/v1/customer-portal/customers/me/payment-methods/"+url.PathEscape(id), nil, nil)
}

// Orders lists the customer's, newest first.
func (p *Polar) Orders(ctx context.Context, token string, limit int) ([]Order, error) {
	var out page[Order]
	path := "/v1/customer-portal/orders/?sorting=-created_at&limit=" + strconv.Itoa(limit)
	if err := p.call(ctx, token, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Order fetches one of the customer's.
func (p *Polar) Order(ctx context.Context, token, id string) (*Order, error) {
	var o Order
	if err := p.call(ctx, token, http.MethodGet, "/v1/customer-portal/orders/"+url.PathEscape(id), nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// GenerateInvoice asks Polar to make an order's invoice; it's ready a few
// seconds later.
func (p *Polar) GenerateInvoice(ctx context.Context, token, id string) error {
	return p.call(ctx, token, http.MethodPost, "/v1/customer-portal/orders/"+url.PathEscape(id)+"/invoice", nil, nil)
}

// InvoiceURL is a link to an order's invoice (a PDF), once generated.
func (p *Polar) InvoiceURL(ctx context.Context, token, id string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := p.call(ctx, token, http.MethodGet, "/v1/customer-portal/orders/"+url.PathEscape(id)+"/invoice", nil, &out); err != nil {
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
