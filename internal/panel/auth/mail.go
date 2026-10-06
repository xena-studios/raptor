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
	"os"
	"time"
)

// Mailer sends transactional email (the provider comes with 3.2's email
// setup; LogMailer is for development).
type Mailer interface {
	Send(ctx context.Context, to, subject, text string) error
}

// LogMailer writes emails to the log instead of sending them, and to File
// as plain text if set (task dev:mail shows it): development only, since
// the codes and links in them sign people in.
type LogMailer struct {
	Log  *slog.Logger
	File string
}

// Send implements Mailer.
func (m LogMailer) Send(_ context.Context, to, subject, text string) error {
	m.Log.Warn("email (not sent: development mailer)", "to", to, "subject", subject, "text", text)
	if m.File == "" {
		return nil
	}
	f, err := os.OpenFile(m.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path from config
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "──── %s ────\nTo: %s\nSubject: %s\n\n%s\n", time.Now().Format(time.TimeOnly), to, subject, text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Verifier checks a bot challenge's token.
type Verifier interface {
	Verify(ctx context.Context, token, ip string) error
}

// Turnstile is Cloudflare Turnstile's server-side check.
type Turnstile struct {
	Secret string
	// Hostname, if set, is where the widget must have been solved: the
	// separate verify page (docs/DECISIONS.md #192), so a token from a widget
	// on some other site with our site key isn't accepted.
	Hostname string
	// URL is siteverify's (Cloudflare's, unless testing).
	URL    string
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
	endpoint := t.URL
	if endpoint == "" {
		endpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode())) //nolint:gosec // Cloudflare's, or a test's
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
		Success  bool     `json:"success"`
		Errors   []string `json:"error-codes"`
		Hostname string   `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if !r.Success {
		return fmt.Errorf("turnstile: %v", r.Errors)
	}
	if t.Hostname != "" && r.Hostname != t.Hostname {
		return fmt.Errorf("turnstile: solved on %q, not %q", r.Hostname, t.Hostname)
	}
	return nil
}
