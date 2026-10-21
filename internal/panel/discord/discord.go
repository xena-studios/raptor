// Package discord is Raptor Bot, on the Raptor Panel Discord server
// (docs/PANEL.md#discord). Its slash commands and buttons arrive at the
// interactions endpoint, on whichever Panel instance Caddy picks; one
// instance at a time holds the gateway connection, which keeps the bot
// online and hears members join; and the Customer role follows who's linked
// and paying. A Discord account is linked when it signs in to a Raptor
// account: there's no other link to keep.
package discord

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

// Bot is Raptor Bot. Every instance serves interactions; Run and Online
// coordinate through Postgres so only one does each job at a time.
type Bot struct {
	// Token is the bot's token; AppID its application's ID; PublicKey the
	// key Discord signs interactions with (all from the Developer Portal).
	Token     string
	AppID     string
	PublicKey ed25519.PublicKey
	// GuildID is the Raptor Panel server; CustomerRole the role for
	// customers, which the bot manages: it adds and removes it to match.
	GuildID      string
	CustomerRole string
	// SupportRole is pinged when a ticket opens ("": nobody is).
	SupportRole string
	// Billing is whether plans decide who's a customer; without billing,
	// it's anyone running a node.
	Billing bool
	// AppURL is the web app's origin and DocsURL the docs', for links.
	AppURL  string
	DocsURL string
	DB      *pgxpool.Pool
	Log     *slog.Logger
	// API and Gateway are Discord's (empty: the real ones); HTTP makes the
	// requests (nil: a client with a 15 second timeout).
	API     string
	Gateway string
	HTTP    *http.Client
	Now     func() time.Time

	once sync.Once
	kick chan struct{}
	docs docsIndex
}

func (b *Bot) init() {
	b.once.Do(func() { b.kick = make(chan struct{}, 1) })
}

func (b *Bot) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bot) log() *slog.Logger {
	if b.Log == nil {
		return slog.Default()
	}
	return b.Log
}

func (b *Bot) api() string {
	if b.API != "" {
		return strings.TrimRight(b.API, "/")
	}
	return "https://discord.com/api/v10"
}

func (b *Bot) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// APIError is Discord's answer to a failed request.
type APIError struct {
	Status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("discord: %d %s (code %d)", e.Status, e.Message, e.Code)
}

// call makes a request to Discord's API as the bot. A rate-limited one is
// tried again after the wait Discord asks for, a few times. reason goes in
// the server's audit log.
func (b *Bot) call(ctx context.Context, method, path string, body, out any, reason string) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, b.api()+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+b.Token)
		req.Header.Set("User-Agent", "DiscordBot (https://raptorpanel.net, "+buildinfo.Version+")")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if reason != "" {
			req.Header.Set("X-Audit-Log-Reason", url.PathEscape(reason))
		}
		resp, err := b.client().Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			var rl struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.Unmarshal(data, &rl)
			wait := time.Duration(rl.RetryAfter * float64(time.Second))
			if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && wait == 0 {
				wait = time.Duration(s * float64(time.Second))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(min(max(wait, 100*time.Millisecond), time.Minute)):
			}
			continue
		}
		if resp.StatusCode >= 300 {
			e := &APIError{Status: resp.StatusCode}
			_ = json.Unmarshal(data, e)
			return e
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

// isUnknown reports whether Discord said the thing asked about doesn't
// exist (a member who left, a deleted message).
func isUnknown(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// PostLinkMessage posts the "Link your Raptor account" message, with its
// button, to a channel (panel discord link-message).
func (b *Bot) PostLinkMessage(ctx context.Context, channelID string) error {
	return b.call(ctx, http.MethodPost, "/channels/"+channelID+"/messages", map[string]any{
		"embeds": []embed{{
			Title:       "Link your Raptor account",
			Description: "Owners and admins of an org with a plan, the free month included, get the **Customer** role. Link the Discord account you're using now to your Raptor account, and it follows your plan from then on.\n\nYour Discord account also signs you in to Raptor once it's linked. Unlink it any time in the app's Settings.",
			Color:       brandColor,
		}},
		"components": []component{row(button{Style: buttonPrimary, Label: "Link my account", CustomID: linkButton})},
	}, nil, "")
}
