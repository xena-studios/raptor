package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/resend/resend-go/v3"
)

// Resend sends the Panel's email through Resend (resend.com), from its own
// sending subdomain (docs/DECISIONS.md #80).
type Resend struct {
	Client *resend.Client
	// From is the sender, as "Raptor <no-reply@mail.raptorpanel.net>".
	From string
}

// NewResend is a Resend mailer with an API key that can only send.
func NewResend(apiKey, from string) *Resend {
	return &Resend{Client: resend.NewCustomClient(&http.Client{Timeout: 15 * time.Second}, apiKey), From: from}
}

// Send implements Mailer. A rate limit or a network error is retried once
// with the same idempotency key, so a send whose answer was lost isn't
// delivered twice.
func (r *Resend) Send(ctx context.Context, to, subject, text string) error {
	req := &resend.SendEmailRequest{From: r.From, To: []string{to}, Subject: subject, Text: text}
	opts := &resend.SendEmailOptions{IdempotencyKey: newToken()}
	_, err := r.Client.Emails.SendWithOptions(ctx, req, opts)
	if err == nil || !retryable(err) {
		return wrapResend(err)
	}
	wait := time.Second
	var rl *resend.RateLimitError
	if errors.As(err, &rl) {
		if s, perr := strconv.Atoi(rl.RetryAfter); perr == nil && s > 0 && s <= 5 {
			wait = time.Duration(s) * time.Second
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	_, err = r.Client.Emails.SendWithOptions(ctx, req, opts)
	return wrapResend(err)
}

func retryable(err error) bool {
	var rl *resend.RateLimitError
	var ue *url.Error
	return errors.As(err, &rl) || errors.As(err, &ue)
}

func wrapResend(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("resend: %w", err)
}
