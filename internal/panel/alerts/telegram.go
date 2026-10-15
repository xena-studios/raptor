package alerts

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TelegramPath is where Telegram delivers the bot's updates.
const TelegramPath = "/alerts/telegram/webhook"

// TelegramBot is Raptor's alerts bot: people add it to a chat with a link
// from the app, and it posts alerts there (docs/PANEL.md#alerts).
type TelegramBot struct {
	Token string
	// WebhookSecret is the secret_token Telegram sends back with every
	// update, so nobody else can post updates to the webhook.
	WebhookSecret string
	// API is Telegram's Bot API (empty: https://api.telegram.org).
	API    string
	Client *http.Client
	// OnStart handles /start with a link token, from a chat; it returns the
	// reply.
	OnStart func(ctx context.Context, chatID int64, chatTitle, token string) string

	mu       sync.RWMutex
	username string // from getMe (Setup)
}

// Username is the bot's, once Setup has worked ("" until then).
func (b *TelegramBot) Username() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.username
}

func (b *TelegramBot) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// telegramError is the Bot API's answer to a failed call.
type telegramError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e telegramError) Error() string { return fmt.Sprintf("telegram: %d %s", e.Code, e.Description) }

func (b *TelegramBot) call(ctx context.Context, method string, body, out any) error {
	api := b.API
	if api == "" {
		api = "https://api.telegram.org"
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/bot"+b.Token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("telegram: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client().Do(req)
	if err != nil {
		// The URL has the bot's token in it: never pass it on.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram unreachable: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("telegram: HTTP %d", res.StatusCode)
	}
	if !r.OK {
		return telegramError{Code: r.ErrorCode, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Setup learns the bot's username and points its webhook at the Panel.
func (b *TelegramBot) Setup(ctx context.Context, webhookURL string) error {
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return err
	}
	b.mu.Lock()
	b.username = me.Username
	b.mu.Unlock()
	return b.call(ctx, "setWebhook", map[string]any{
		"url": webhookURL, "secret_token": b.WebhookSecret,
		"allowed_updates": []string{"message"}, "drop_pending_updates": true,
	}, nil)
}

// Send posts a message (HTML) to a chat. A chat that's gone, or that
// removed the bot, is a permanent failure.
func (b *TelegramBot) Send(ctx context.Context, chatID int64, text string) error {
	err := b.call(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}, nil)
	var te telegramError
	if errors.As(err, &te) && (te.Code == 400 || te.Code == 403) {
		return permanent{err}
	}
	return err
}

// LinkURL is the link that opens a chat with the bot and sends /start with
// the token; group adds the bot to a group instead.
func (b *TelegramBot) LinkURL(token string, group bool) string {
	if group {
		return "https://t.me/" + b.Username() + "?startgroup=" + token
	}
	return "https://t.me/" + b.Username() + "?start=" + token
}

// ServeHTTP takes the bot's updates from Telegram.
func (b *TelegramBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if b.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(b.WebhookSecret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var u struct {
		Message *struct {
			Text string `json:"text"`
			Chat struct {
				ID        int64  `json:"id"`
				Type      string `json:"type"`
				Title     string `json:"title"`
				Username  string `json:"username"`
				FirstName string `json:"first_name"`
			} `json:"chat"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&u); err != nil || u.Message == nil {
		w.WriteHeader(http.StatusOK) // nothing for us; don't make Telegram retry
		return
	}
	cmd, token, _ := strings.Cut(strings.TrimSpace(u.Message.Text), " ")
	if name, _, ok := strings.Cut(cmd, "@"); ok {
		cmd = name
	}
	if cmd != "/start" {
		w.WriteHeader(http.StatusOK)
		return
	}
	c := u.Message.Chat
	title := c.Title
	switch {
	case title != "":
	case c.Username != "":
		title = "@" + c.Username
	default:
		title = c.FirstName
	}
	reply := "Hi. To get Raptor alerts here, open your org's settings in Raptor, go to Alerts, and use Connect Telegram."
	if token = strings.TrimSpace(token); token != "" && b.OnStart != nil {
		reply = b.OnStart(r.Context(), c.ID, title, token)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	_ = b.call(ctx, "sendMessage", map[string]any{"chat_id": c.ID, "text": reply}, nil)
	w.WriteHeader(http.StatusOK)
}
