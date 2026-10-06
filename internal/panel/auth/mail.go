package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Mailer sends transactional email (the provider comes with 3.2's email
// setup; LogMailer is for development).
type Mailer interface {
	Send(ctx context.Context, to, subject, text string) error
}

// LogMailer writes emails to the log instead of sending them: development
// only, since the codes and links in them sign people in.
type LogMailer struct{ Log *slog.Logger }

// Send implements Mailer.
func (m LogMailer) Send(_ context.Context, to, subject, text string) error {
	m.Log.Warn("email (not sent: development mailer)", "to", to, "subject", subject, "text", text)
	return nil
}

// Verifier checks a bot challenge's token.
type Verifier interface {
	Verify(ctx context.Context, token, ip string) error
}

// Turnstile is Cloudflare Turnstile's server-side check.
type Turnstile struct {
	Secret string
	Client *http.Client
}

// Verify implements Verifier.
func (t Turnstile) Verify(ctx context.Context, token, ip string) error {
	if token == "" {
		return errors.New("no token")
	}
	form := url.Values{"secret": {t.Secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Success bool     `json:"success"`
		Errors  []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if !r.Success {
		return fmt.Errorf("turnstile: %v", r.Errors)
	}
	return nil
}
