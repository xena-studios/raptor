// Package notify sends notifications straight from the node (Discord and
// generic webhooks), so they work while the Panel is unreachable, and so a
// compromised Panel can't hide signed dangerous actions from the owner
// (docs/WINGS.md#notifications). Targets come from config.yml on the box.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Cursors: how far notifications have been sent, so a Wings restart neither
// loses nor repeats them.
const (
	eventCursor = "notify.event_seq"
	auditCursor = "notify.audit_id"
)

// Delivery limits.
const (
	pollEvery = 2 * time.Second
	// Each send is tried this many times, waiting retryBase, 4×, 16×… between.
	attempts  = 4
	retryBase = 2 * time.Second
	// perMinute messages per target; more are counted and summarized in the
	// next one sent (a crash loop shouldn't flood a channel).
	perMinute = 20
	batch     = 100
)

// Notifier sends notifications to the configured targets.
type Notifier struct {
	Targets []config.Notification
	DB      *store.DB
	Outbox  *events.Outbox
	// Node names the node in messages (its hostname).
	Node   string
	NodeID string
	Log    *slog.Logger
	Client *http.Client
	Now    func() time.Time
	// RetryBase overrides retryBase (tests).
	RetryBase time.Duration

	wake    chan struct{}
	limitMu sync.Mutex
	limits  map[int]*limiter
}

type limiter struct {
	window     time.Time
	sent       int
	suppressed int
	// downUntil: the target failed every retry; it's skipped until then, so
	// one dead target doesn't hold up the others.
	downUntil time.Time
}

// downFor is how long a target that failed every retry is skipped.
const downFor = 5 * time.Minute

func (n *Notifier) init() {
	if n.wake == nil {
		n.wake = make(chan struct{}, 1)
	}
	if n.limits == nil {
		n.limits = map[int]*limiter{}
	}
	if n.Log == nil {
		n.Log = slog.New(slog.DiscardHandler)
	}
	if n.Client == nil {
		n.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.RetryBase == 0 {
		n.RetryBase = retryBase
	}
}

// Wake makes the notifier look for new events now.
func (n *Notifier) Wake() {
	n.init()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// Run sends notifications until ctx ends. On the very first run it starts
// from now: earlier events aren't sent.
func (n *Notifier) Run(ctx context.Context) {
	n.init()
	if len(n.Targets) == 0 {
		return
	}
	if err := n.initCursors(ctx); err != nil {
		n.Log.Error("notifications: can't read their progress", "err", err)
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		n.Process(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-n.wake:
		}
	}
}

func (n *Notifier) cursor(ctx context.Context, key string) (int64, bool, error) {
	v, err := n.DB.Write.GetKV(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	c, err := strconv.ParseInt(string(v), 10, 64)
	return c, true, err
}

func (n *Notifier) setCursor(ctx context.Context, key string, v int64) error {
	return n.DB.Write.SetKV(context.WithoutCancel(ctx), store.SetKVParams{Key: key, Value: []byte(strconv.FormatInt(v, 10))})
}

func (n *Notifier) initCursors(ctx context.Context) error {
	if _, ok, err := n.cursor(ctx, eventCursor); err != nil || !ok {
		if err != nil {
			return err
		}
		last, err := n.Outbox.Last(ctx)
		if err != nil {
			return err
		}
		if err := n.setCursor(ctx, eventCursor, last); err != nil {
			return err
		}
	}
	if _, ok, err := n.cursor(ctx, auditCursor); err != nil || !ok {
		if err != nil {
			return err
		}
		list, err := command.ListAudit(ctx, n.DB, time.Time{}, 1)
		if err != nil {
			return err
		}
		var last int64
		if len(list) > 0 {
			last = list[0].ID
		}
		return n.setCursor(ctx, auditCursor, last)
	}
	return nil
}

// Process sends everything new since the cursors and advances them.
func (n *Notifier) Process(ctx context.Context) {
	n.init()
	if err := n.processEvents(ctx); err != nil && ctx.Err() == nil {
		n.Log.Error("notifications: reading events failed", "err", err)
	}
	if err := n.processAudit(ctx); err != nil && ctx.Err() == nil {
		n.Log.Error("notifications: reading the audit log failed", "err", err)
	}
}

func (n *Notifier) processEvents(ctx context.Context) error {
	seq, _, err := n.cursor(ctx, eventCursor)
	if err != nil {
		return err
	}
	list, err := n.Outbox.Since(ctx, seq, batch)
	if errors.Is(err, events.ErrGap) {
		// Events were pruned before they were sent (the node was off the
		// Panel for a long time, or notifications were stuck): carry on
		// from the oldest one left.
		n.Log.Warn("notifications: some events were pruned before they could be sent")
		list, err = n.Outbox.Since(ctx, 0, batch)
	}
	if err != nil {
		return err
	}
	for _, e := range list {
		if m, ok := FromEvent(e); ok {
			n.send(ctx, n.named(ctx, m))
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // stopping; the cursor stays here
		}
		if err := n.setCursor(ctx, eventCursor, e.Seq); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) processAudit(ctx context.Context) error {
	id, _, err := n.cursor(ctx, auditCursor)
	if err != nil {
		return err
	}
	list, err := command.ListAuditAfter(ctx, n.DB, id, batch)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Outcome == command.AuditRunning {
			return nil // wait for its outcome, keeping the order
		}
		n.send(ctx, n.named(ctx, FromAudit(a)))
		if ctx.Err() != nil {
			return nil //nolint:nilerr // stopping; the cursor stays here
		}
		if err := n.setCursor(ctx, auditCursor, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// named fills in the server's name.
func (n *Notifier) named(ctx context.Context, m Message) Message {
	if m.ServerID != "" {
		if s, err := n.DB.Read.GetServer(ctx, m.ServerID); err == nil {
			m.Server = s.Name
		}
	}
	return m
}

// wants reports whether a target takes a category.
func wants(t config.Notification, category string) bool {
	return len(t.Events) == 0 || slices.Contains(t.Events, category)
}

// send delivers a message to every target that wants it.
func (n *Notifier) send(ctx context.Context, m Message) {
	for i, t := range n.Targets {
		if !wants(t, m.Category) {
			continue
		}
		suppressed, ok := n.allow(i)
		if !ok {
			continue
		}
		mm := m
		if suppressed > 0 {
			mm.Text += fmt.Sprintf("\n(%d earlier notifications weren't sent: more than %d a minute, or this target was unreachable.)", suppressed, perMinute)
		}
		if err := n.deliver(ctx, t, mm); err != nil && ctx.Err() == nil {
			n.Log.Error("notification not delivered", "target", targetName(t, i), "type", m.Type, "err", err)
			n.limitMu.Lock()
			n.limits[i].suppressed += 1 + suppressed
			var perm permanent
			if !errors.As(err, &perm) {
				n.limits[i].downUntil = n.Now().Add(downFor)
			}
			n.limitMu.Unlock()
		}
	}
}

// allow applies the per-target rate limit. It returns how many messages were
// suppressed since the last one sent.
func (n *Notifier) allow(i int) (int, bool) {
	n.limitMu.Lock()
	defer n.limitMu.Unlock()
	l := n.limits[i]
	if l == nil {
		l = &limiter{}
		n.limits[i] = l
	}
	now := n.Now()
	if now.Before(l.downUntil) {
		l.suppressed++
		return 0, false
	}
	if now.Sub(l.window) >= time.Minute {
		l.window, l.sent = now, 0
	}
	if l.sent >= perMinute {
		l.suppressed++
		return 0, false
	}
	l.sent++
	s := l.suppressed
	l.suppressed = 0
	return s, true
}

func targetName(t config.Notification, i int) string {
	if t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("%s #%d", t.Type, i+1)
}

// deliver sends one message to one target, retrying with backoff.
func (n *Notifier) deliver(ctx context.Context, t config.Notification, m Message) error {
	body, err := n.body(t, m)
	if err != nil {
		return err
	}
	wait := n.RetryBase
	for try := 1; ; try++ {
		err = n.post(ctx, t, body, m)
		var perm permanent
		if err == nil || errors.As(err, &perm) || try == attempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 4
	}
}

// permanent is a failure retrying won't fix (a 4xx other than 429).
type permanent struct{ error }

func (n *Notifier) post(ctx context.Context, t config.Notification, body []byte, m Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Raptor-Wings/"+buildinfo.Version)
	if t.Type == "webhook" {
		ts := strconv.FormatInt(n.Now().Unix(), 10)
		req.Header.Set("X-Raptor-Event", m.Type)
		req.Header.Set("X-Raptor-Timestamp", ts)
		if t.Secret != "" {
			req.Header.Set("X-Raptor-Signature", "sha256="+Sign(t.Secret, ts, body))
		}
	}
	res, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	default:
		return permanent{fmt.Errorf("HTTP %d (check the URL)", res.StatusCode)}
	}
}

// Sign is a webhook body's signature: hex HMAC-SHA256 of
// "<timestamp>.<body>" with the target's secret. Receivers recompute it and
// reject old timestamps, so a captured request can't be replayed later.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Discord embed colors per level.
var colors = map[string]int{Info: 0x3b82f6, Warning: 0xf59e0b, Critical: 0xef4444}

func (n *Notifier) body(t config.Notification, m Message) ([]byte, error) {
	if t.Type == "webhook" {
		return json.Marshal(struct {
			Message
			Node   string `json:"node"`
			NodeID string `json:"node_id,omitempty"`
		}{m, n.Node, n.NodeID})
	}
	desc := m.Text
	if len(desc) > 3500 {
		desc = desc[:3500] + "…"
	}
	fields := []map[string]any{{"name": "Node", "value": orDash(n.Node), "inline": true}}
	if m.ServerID != "" {
		name := m.Server
		if name == "" {
			name = m.ServerID
		}
		fields = append(fields, map[string]any{"name": "Server", "value": name, "inline": true})
	}
	return json.Marshal(map[string]any{
		"username":         "Raptor",
		"allowed_mentions": map[string]any{"parse": []string{}},
		"embeds": []map[string]any{{
			"title": m.Title, "description": desc, "color": colors[m.Level],
			"fields": fields, "timestamp": m.At.UTC().Format(time.RFC3339),
			"footer": map[string]any{"text": m.Category},
		}},
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Result is how a test notification went for one target.
type Result struct {
	Target string
	Err    error
}

// Test sends a test message to every target, without retries.
func (n *Notifier) Test(ctx context.Context) []Result {
	n.init()
	m := Message{
		Category: "test", Level: Info, Type: "test", Title: "Test notification", At: n.Now(),
		Text: "Notifications from this node work. You'll get the categories set for this target in /etc/raptor/config.yml.",
	}
	out := make([]Result, 0, len(n.Targets))
	for i, t := range n.Targets {
		body, err := n.body(t, m)
		if err == nil {
			err = n.post(ctx, t, body, m)
		}
		out = append(out, Result{Target: targetName(t, i), Err: err})
	}
	return out
}
