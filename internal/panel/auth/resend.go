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

	"github.com/xena-studios/raptor/internal/panel/mail"
)

// Resend sends the Panel's email through Resend (resend.com), from its own
// sending subdomain (docs/DECISIONS.md #80).
type Resend struct {
	Client *resend.Client
	// From is the sender, as "Raptor <account@mail.raptorpanel.net>": a
	// real-looking address, not no-reply, which costs trust.
	From string
	// ReplyTo is where answers go (support@raptorpanel.net), so someone
	// writing back "this wasn't me" reaches a person; empty: the sender.
	ReplyTo string
}

// NewResend is a Resend mailer with an API key that can only send.
func NewResend(apiKey, from string) *Resend {
	return &Resend{Client: resend.NewCustomClient(&http.Client{Timeout: 15 * time.Second}, apiKey), From: from}
}

// Send implements Mailer: the text and the HTML versions, with the logo
// inline. A rate limit or a network error is retried once with the same
// idempotency key, so a send whose answer was lost isn't delivered twice.
func (r *Resend) Send(ctx context.Context, to string, e mail.Email) error {
	req := &resend.SendEmailRequest{
		From: r.From, To: []string{to}, Subject: e.Subject, Text: e.Text(), Html: e.HTML(), ReplyTo: r.ReplyTo,
		Attachments: []*resend.Attachment{{Content: mail.Logo, Filename: "raptor.png", ContentType: "image/png", ContentId: mail.LogoID}},
	}
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
