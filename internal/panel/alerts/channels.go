package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

// Channel types.
const (
	Email    = "email"
	Discord  = "discord"
	Slack    = "slack"
	Telegram = "telegram"
	Ntfy     = "ntfy"
	Webhook  = "webhook"
)

// DefaultNtfy is the public ntfy server.
const DefaultNtfy = "https://ntfy.sh"

// ChannelInput is a channel as an admin enters it.
type ChannelInput struct {
	Type string
	Name string
	// URL: a Discord or Slack webhook, or a webhook's endpoint.
	URL string
	// Secret signs a webhook's requests (optional).
	Secret string
	// ntfy: the server (empty: ntfy.sh), the topic, and an access token
	// (optional).
	NtfyServer, NtfyTopic, NtfyToken string
}

// config is what a channel keeps in the clear.
type config struct {
	To        string `json:"to,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`
	ChatTitle string `json:"chat_title,omitempty"`
	Server    string `json:"server,omitempty"`
}

// secret is what a channel keeps sealed.
type secret struct {
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"`
	Topic  string `json:"topic,omitempty"`
	Token  string `json:"token,omitempty"`
}

// InputError is a channel or rule the admin got wrong; its text is for
// them.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

var (
	discordPath = regexp.MustCompile(`^/api/webhooks/\d+/[\w-]+$`)
	slackPath   = regexp.MustCompile(`^/services/[\w/]+$`)
	ntfyTopic   = regexp.MustCompile(`^[\w-]{1,64}$`)
)

// parseHTTPS parses a URL that must be https (or http when private
// addresses are allowed, for development).
func parseHTTPS(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return nil, invalid("that isn't a URL")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP) {
		return nil, invalid("the URL must start with https://")
	}
	return u, nil
}

// prepare checks a channel and splits it into what's kept in the clear,
// what's sealed, and the hint shown in place of the secret.
func prepare(in ChannelInput, allowPrivate bool) (config, secret, string, error) {
	var c config
	var s secret
	switch in.Type {
	case Email:
		// Someone's own email: their account's address.
		c.To = "me"
		return c, s, "", nil
	case Discord:
		u, err := parseHTTPS(in.URL, false)
		if err != nil {
			return c, s, "", err
		}
		switch u.Host {
		case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
		default:
			return c, s, "", invalid("that isn't a Discord webhook URL (Server Settings, Integrations, Webhooks, Copy Webhook URL)")
		}
		if !discordPath.MatchString(u.Path) {
			return c, s, "", invalid("that isn't a Discord webhook URL (Server Settings, Integrations, Webhooks, Copy Webhook URL)")
		}
		s.URL = "https://discord.com" + u.Path
		return c, s, "discord.com" + u.Path[:min(len(u.Path), len("/api/webhooks/")+6)] + "…", nil
	case Slack:
		u, err := parseHTTPS(in.URL, false)
		if err != nil {
			return c, s, "", err
		}
		if u.Host != "hooks.slack.com" || !slackPath.MatchString(u.Path) {
			return c, s, "", invalid("that isn't a Slack incoming webhook URL (https://hooks.slack.com/services/…)")
		}
		s.URL = "https://hooks.slack.com" + u.Path
		return c, s, "hooks.slack.com/services/" + u.Path[len("/services/"):min(len(u.Path), len("/services/")+4)] + "…", nil
	case Webhook:
		u, err := parseHTTPS(in.URL, allowPrivate)
		if err != nil {
			return c, s, "", err
		}
		if len(in.Secret) > 256 {
			return c, s, "", invalid("the signing secret is too long")
		}
		s.URL, s.Secret = u.String(), in.Secret
		return c, s, u.Host + "/…", nil
	case Ntfy:
		server := strings.TrimRight(strings.TrimSpace(in.NtfyServer), "/")
		if server == "" {
			server = DefaultNtfy
		}
		u, err := parseHTTPS(server, allowPrivate)
		if err != nil {
			return c, s, "", err
		}
		if u.Path != "" || u.RawQuery != "" {
			return c, s, "", invalid("the ntfy server is just its address, like https://ntfy.sh")
		}
		topic := strings.TrimSpace(in.NtfyTopic)
		if !ntfyTopic.MatchString(topic) {
			return c, s, "", invalid("the topic is letters, digits, - and _ (up to 64)")
		}
		c.Server, s.Topic, s.Token = u.String(), topic, strings.TrimSpace(in.NtfyToken)
		return c, s, u.Host + "/" + topic[:min(len(topic), 3)] + "…", nil
	case Telegram:
		return c, s, "", invalid("Telegram chats are added with Connect Telegram")
	}
	return c, s, "", invalid("unknown channel type %q", in.Type)
}

// permanent is a failure retrying won't fix.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// target is a channel ready to send to.
type target struct {
	Type   string
	Config config
	Secret secret
	// Emails for an email channel.
	Emails []string
}

// sender sends alerts; Service has one.
type sender struct {
	client   *http.Client
	mail     func(ctx context.Context, to, subject, body string) error
	telegram *TelegramBot
	now      func() time.Time
}

// send delivers one alert to one channel.
func (s *sender) send(ctx context.Context, t target, m Message) error {
	switch t.Type {
	case Email:
		if s.mail == nil {
			return errors.New("email isn't set up on this Panel")
		}
		if len(t.Emails) == 0 {
			return permanent{errors.New("nobody to email: no member of the org matches")}
		}
		subject, body := emailText(m)
		var errs []error
		for _, to := range t.Emails {
			if err := s.mail(ctx, to, subject, body); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", to, err))
			}
		}
		return errors.Join(errs...)
	case Discord:
		return s.post(ctx, t.Secret.URL, discordBody(m), nil)
	case Slack:
		return s.post(ctx, t.Secret.URL, slackBody(m), nil)
	case Webhook:
		body, err := json.Marshal(m)
		if err != nil {
			return permanent{err}
		}
		ts := strconv.FormatInt(s.now().Unix(), 10)
		h := http.Header{"X-Raptor-Event": {m.Type}, "X-Raptor-Timestamp": {ts}}
		if t.Secret.Secret != "" {
			h.Set("X-Raptor-Signature", "sha256="+Sign(t.Secret.Secret, ts, body))
		}
		return s.post(ctx, t.Secret.URL, body, h)
	case Ntfy:
		body, _ := json.Marshal(ntfyBody(t.Secret.Topic, m))
		h := http.Header{}
		if t.Secret.Token != "" {
			h.Set("Authorization", "Bearer "+t.Secret.Token)
		}
		return s.post(ctx, t.Config.Server, body, h)
	case Telegram:
		if s.telegram == nil {
			return errors.New("this Panel has no Telegram bot")
		}
		return s.telegram.Send(ctx, t.Config.ChatID, telegramText(m))
	}
	return permanent{fmt.Errorf("unknown channel type %q", t.Type)}
}

func (s *sender) post(ctx context.Context, to string, body []byte, h http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to, bytes.NewReader(body))
	if err != nil {
		return permanent{err}
	}
	for k, v := range h {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Raptor/"+buildinfo.Version)
	res, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, errPrivate) {
			return permanent{errPrivate}
		}
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return permanent{fmt.Errorf("HTTP %d: redirects aren't followed; use the final URL", res.StatusCode)}
	default:
		return permanent{fmt.Errorf("HTTP %d (check the URL)", res.StatusCode)}
	}
}

// Sign is a webhook body's signature: hex HMAC-SHA256 of
// "<timestamp>.<body>" with the channel's secret. Receivers recompute it
// and reject old timestamps, so a captured request can't be replayed.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// where says what an alert is about: "smp on node-1".
func where(m Message) string {
	switch {
	case m.Server != "" && m.Node != "":
		return m.Server + " on " + m.Node
	case m.Server != "":
		return m.Server
	}
	return m.Node
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func emailText(m Message) (string, string) {
	subject := "Raptor: " + m.Title
	if w := where(m); w != "" {
		subject += " (" + w + ")"
	}
	var b strings.Builder
	b.WriteString(m.Title + "\n\n")
	if m.Text != "" {
		b.WriteString(m.Text + "\n\n")
	}
	if m.Server != "" {
		b.WriteString("Server: " + m.Server + "\n")
	}
	if m.Node != "" {
		b.WriteString("Node: " + m.Node + "\n")
	}
	if m.Org != "" {
		b.WriteString("Org: " + m.Org + "\n")
	}
	b.WriteString("When: " + m.At.UTC().Format("2 Jan 2006 15:04 UTC") + "\n")
	if m.URL != "" {
		b.WriteString("\n" + m.URL + "\n")
	}
	if m.Condition == "test" {
		b.WriteString("\nYou sent a test to your email from Raptor. Change how you're told about alerts from the bell at the top of any page.\n")
		return subject, b.String()
	}
	b.WriteString("\nYou get this because of an alert rule")
	if m.Rule != "" {
		b.WriteString(" (" + m.Rule + ")")
	}
	b.WriteString(" in this org that tells you. Change how you're told from the bell at the top of any page in Raptor; the org's admins choose which alerts you get.\n")
	return subject, b.String()
}

// Discord embed colors per level.
var colors = map[string]int{Info: 0x3b82f6, Warning: 0xf59e0b, Critical: 0xef4444}

func discordBody(m Message) []byte {
	fields := []map[string]any{}
	if m.Server != "" {
		fields = append(fields, map[string]any{"name": "Server", "value": m.Server, "inline": true})
	}
	if m.Node != "" {
		fields = append(fields, map[string]any{"name": "Node", "value": m.Node, "inline": true})
	}
	color := colors[m.Level]
	if m.Resolved {
		color = 0x22c55e
	}
	embed := map[string]any{
		"title": truncate(m.Title, 250), "description": truncate(m.Text, 3500), "color": color,
		"fields": fields, "timestamp": m.At.UTC().Format(time.RFC3339),
		"footer": map[string]any{"text": strings.TrimSpace(m.Org + " · " + m.Condition)},
	}
	if m.URL != "" {
		embed["url"] = m.URL
	}
	b, _ := json.Marshal(map[string]any{
		"username":         "Raptor",
		"allowed_mentions": map[string]any{"parse": []string{}},
		"embeds":           []map[string]any{embed},
	})
	return b
}

// slackEscape escapes Slack's mrkdwn control characters.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func slackBody(m Message) []byte {
	text := "*" + slackEscape(m.Title) + "*"
	if w := where(m); w != "" {
		text += "  ·  " + slackEscape(w)
	}
	if m.Text != "" {
		text += "\n" + slackEscape(truncate(m.Text, 2500))
	}
	if m.URL != "" {
		text += "\n<" + m.URL + "|Open in Raptor>"
	}
	b, _ := json.Marshal(map[string]any{"text": text, "unfurl_links": false})
	return b
}

func ntfyBody(topic string, m Message) map[string]any {
	priority := map[string]int{Info: 3, Warning: 4, Critical: 5}[m.Level]
	tags := []string{"warning"}
	switch {
	case m.Resolved:
		tags = []string{"white_check_mark"}
	case m.Level == Info:
		tags = []string{"information_source"}
	case m.Level == Critical:
		tags = []string{"rotating_light"}
	}
	msg := m.Text
	if w := where(m); w != "" {
		msg = strings.TrimSpace(w + "\n" + msg)
	}
	out := map[string]any{"topic": topic, "title": truncate(m.Title, 200), "message": truncate(msg, 3500), "priority": priority, "tags": tags}
	if m.URL != "" {
		out["click"] = m.URL
	}
	return out
}

func telegramText(m Message) string {
	icon := map[string]string{Info: "ℹ️", Warning: "⚠️", Critical: "🚨"}[m.Level]
	if m.Resolved {
		icon = "✅"
	}
	var b strings.Builder
	b.WriteString(icon + " <b>" + html.EscapeString(m.Title) + "</b>")
	if w := where(m); w != "" {
		b.WriteString("\n" + html.EscapeString(w))
	}
	if m.Text != "" {
		b.WriteString("\n\n" + html.EscapeString(truncate(m.Text, 3000)))
	}
	if m.URL != "" {
		b.WriteString("\n\n<a href=\"" + html.EscapeString(m.URL) + "\">Open in Raptor</a>")
	}
	return b.String()
}
