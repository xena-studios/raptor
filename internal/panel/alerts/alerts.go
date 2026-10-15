// Package alerts sends an org's alerts (docs/PANEL.md#alerts): rules say
// what's worth one (a crash, a finished install, a node offline, CPU above a
// threshold, …), channels say where it goes (email, Discord, Slack,
// Telegram, ntfy, a webhook).
//
// Most alerts come from nodes' events, which the mirror reads in order: it
// hands each batch to Ingest in the same transaction that applies it, so an
// alert is queued exactly when the event is. Thresholds are evaluated on the
// nodes, which have the metrics, from the rules SyncThresholds sends them;
// they come back as alert.fired and alert.resolved events. The Panel watches
// for nodes going offline itself. One Panel instance at a time sends what's
// queued.
package alerts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Limits and timing.
const (
	ChannelsPerOrg = 20
	RulesPerOrg    = 100
	// perMinute alerts go to a channel a minute; more are dropped and
	// counted in the next one sent (a crash loop shouldn't flood a chat).
	perMinute = 20
	// attempts at sending each alert, waiting retryAfter[i] after the i-th.
	attempts = 4
	// failingFor is how long a channel fails every alert before it's turned
	// off and the org's owners are told.
	failingFor = 3 * 24 * time.Hour
	// staleAfter: events older than this when the mirror gets them (a node
	// that was cut off for a long time) don't alert; it's old news.
	staleAfter = 6 * time.Hour
	// TelegramLinkTTL is how long a Connect Telegram link works.
	TelegramLinkTTL = 15 * time.Minute
	// UserID is who the Panel's node.alerts commands are from.
	UserID = "panel:alerts"
	// lockID is the advisory lock the sender holds, so one Panel instance
	// sends at a time.
	lockID = 0x52_41_4c_45_52_54 // "RALERT"
)

var retryAfter = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Service queues and sends alerts.
type Service struct {
	DB *pgxpool.Pool
	// DataKey seals channels' secrets (PANEL_DATA_KEY); without it, only
	// email and Telegram channels can be added.
	DataKey []byte
	// Mail sends an email (auth.Mailer's Send); nil: email channels fail.
	Mail     func(ctx context.Context, to, subject, body string) error
	Telegram *TelegramBot
	// AppURL is the web app, for links in alerts.
	AppURL string
	// Nodes and PanelKey send threshold rules to nodes.
	Nodes    Sender
	PanelKey ed25519.PrivateKey
	// AllowPrivate lets channels reach private addresses (development and
	// tests only).
	AllowPrivate bool
	Log          *slog.Logger
	Now          func() time.Time

	once   sync.Once
	send   *sender
	mu     sync.Mutex
	limits map[pgtype.UUID]*limiter
	syncCh chan struct{}
	// noticed is when this instance first saw each node offline (by rule
	// and node): the grace counts from then as well as from when the node
	// was last seen, so a Panel restart, whose nodes are briefly held by no
	// live instance, isn't taken for nodes going offline.
	noticed map[[2]pgtype.UUID]time.Time
}

type limiter struct {
	window     time.Time
	sent       int
	suppressed int
}

func (s *Service) init() {
	s.once.Do(func() {
		if s.Log == nil {
			s.Log = slog.New(slog.DiscardHandler)
		}
		if s.Now == nil {
			s.Now = time.Now
		}
		s.send = &sender{client: safeClient(s.AllowPrivate), mail: s.Mail, telegram: s.Telegram, now: s.Now}
		s.limits = map[pgtype.UUID]*limiter{}
		s.noticed = map[[2]pgtype.UUID]time.Time{}
		s.syncCh = make(chan struct{}, 1)
	})
}

// Ingest queues the alerts a batch of a node's events raises, in the
// mirror's transaction.
func (s *Service) Ingest(ctx context.Context, q *store.Queries, node pgtype.UUID, evs []*nodev1.Event) error {
	s.init()
	type alert struct {
		m    Message
		rule string
	}
	var found []alert
	users := map[pgtype.UUID]bool{}
	for _, e := range evs {
		at := time.UnixMilli(e.GetAt())
		if s.Now().Sub(at) > staleAfter {
			continue
		}
		var data map[string]any
		if len(e.GetData()) > 0 {
			if err := json.Unmarshal(e.GetData(), &data); err != nil {
				continue
			}
		}
		m, rule, ok := FromEvent(Event{Type: e.GetType(), ServerID: e.GetServerId(), At: at, Data: data})
		if !ok {
			continue
		}
		if m.Condition == "security" {
			if id, err := uuid.Parse(str(data, "user_id")); err == nil {
				users[pgtype.UUID{Bytes: id, Valid: true}] = true
			}
		}
		found = append(found, alert{m, rule})
	}
	if len(found) == 0 {
		return nil
	}
	rules, err := q.AlertRulesForNode(ctx, node)
	if err != nil || len(rules) == 0 {
		return err
	}
	n, err := q.AlertNode(ctx, node)
	if err != nil {
		return err
	}
	org, err := q.OrgName(ctx, n.OrgID)
	if err != nil {
		return err
	}
	names := map[string]string{}
	servers, err := q.AlertServerNames(ctx, node)
	if err != nil {
		return err
	}
	for _, r := range servers {
		names[r.ServerID] = r.Name
	}
	who := map[string]string{}
	if len(users) > 0 {
		rows, err := q.UserNames(ctx, slices.Collect(mapKeys(users)))
		if err != nil {
			return err
		}
		for _, r := range rows {
			who[uuid.UUID(r.ID.Bytes).String()] = r.Name
		}
	}
	active := map[pgtype.UUID][]pgtype.UUID{}
	for _, a := range found {
		m := a.m
		m.Org, m.Node, m.NodeID = org, n.Name, uuid.UUID(node.Bytes).String()
		m.Server = names[m.ServerID]
		if m.Condition == "security" {
			if name := who[str(m.Data, "user_id")]; name != "" {
				d := maps(m.Data)
				d["user"] = name
				securityText(&m, d)
			}
		}
		m.URL = s.link(n.OrgID, m.NodeID, m.ServerID)
		for _, r := range rules {
			if r.Condition != m.Condition || (a.rule != "" && uuid.UUID(r.ID.Bytes).String() != a.rule) {
				continue
			}
			if r.ServerID != "" && r.ServerID != m.ServerID {
				continue
			}
			if m.Resolved && !r.NotifyResolved {
				continue
			}
			chans, ok := active[r.ID]
			if !ok {
				if chans, err = q.ActiveAlertChannels(ctx, r.ChannelIds); err != nil {
					return err
				}
				active[r.ID] = chans
			}
			mm := m
			mm.Rule = r.Name
			if err := s.queue(ctx, q, n.OrgID, r.ID, chans, mm); err != nil {
				return err
			}
		}
	}
	return nil
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func maps(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Service) link(org pgtype.UUID, node, server string) string {
	if s.AppURL == "" {
		return ""
	}
	base := s.AppURL + "/orgs/" + uuid.UUID(org.Bytes).String()
	switch {
	case server != "" && node != "":
		return base + "/servers/" + node + "/" + server
	case node != "":
		return base + "/nodes/" + node
	}
	return base + "/settings/alerts"
}

func (s *Service) queue(ctx context.Context, q *store.Queries, org, rule pgtype.UUID, chans []pgtype.UUID, m Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if err := q.InsertAlertDelivery(ctx, store.InsertAlertDeliveryParams{OrgID: org, RuleID: rule, ChannelID: c, Message: body}); err != nil {
			return err
		}
	}
	return nil
}

// Run sends alerts and watches nodes until ctx ends; every Panel instance
// runs it, and one at a time does the work.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	s.init()
	t := time.NewTicker(every)
	defer t.Stop()
	var lastSync, lastPrune time.Time
	for {
		forceSync := false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.syncCh:
			forceSync = true
		}
		s.tick(ctx, forceSync || s.Now().Sub(lastSync) >= time.Minute, s.Now().Sub(lastPrune) >= time.Hour)
		if forceSync || s.Now().Sub(lastSync) >= time.Minute {
			lastSync = s.Now()
		}
		if s.Now().Sub(lastPrune) >= time.Hour {
			lastPrune = s.Now()
		}
	}
}

// RulesChanged asks for the threshold rules to be sent to nodes now.
func (s *Service) RulesChanged() {
	s.init()
	select {
	case s.syncCh <- struct{}{}:
	default:
	}
}

func (s *Service) tick(ctx context.Context, syncNodes, prune bool) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()
	if err := s.Offline(ctx); err != nil && ctx.Err() == nil {
		s.Log.Error("alerts: checking for offline nodes failed", "err", err)
	}
	if syncNodes {
		s.SyncThresholds(ctx)
	}
	if err := s.Deliver(ctx); err != nil && ctx.Err() == nil {
		s.Log.Error("alerts: sending failed", "err", err)
	}
	if prune {
		q := store.New(s.DB)
		if err := q.PruneAlertDeliveries(ctx); err != nil {
			s.Log.Warn("alerts: pruning history failed", "err", err)
		}
		if err := q.PruneTelegramLinks(ctx); err != nil {
			s.Log.Warn("alerts: pruning Telegram links failed", "err", err)
		}
	}
}

// Offline fires node_offline alerts for nodes gone past their rule's
// grace, and resolves the ones that are back.
func (s *Service) Offline(ctx context.Context) error {
	s.init()
	q := store.New(s.DB)
	offline, err := q.AlertOfflineNodes(ctx)
	if err != nil {
		return err
	}
	now := s.Now()
	seen := map[[2]pgtype.UUID]bool{}
	var gone []store.AlertOfflineNodesRow
	for _, o := range offline {
		k := [2]pgtype.UUID{o.RuleID, o.NodeID}
		seen[k] = true
		if _, ok := s.noticed[k]; !ok {
			s.noticed[k] = now
		}
		grace := time.Duration(max(o.ForMinutes, 1)) * time.Minute
		if now.Sub(o.LastSeenAt.Time) >= grace && now.Sub(s.noticed[k]) >= grace {
			gone = append(gone, o)
		}
	}
	for k := range s.noticed {
		if !seen[k] {
			delete(s.noticed, k)
		}
	}
	for _, g := range gone {
		org, _ := q.OrgName(ctx, g.OrgID)
		node := uuid.UUID(g.NodeID.Bytes).String()
		m := Message{
			Condition: "node_offline", Level: Critical, Type: "node.offline", Title: "Node offline",
			Text: fmt.Sprintf("It hasn't been connected to Raptor since %s. Its game servers may still be running; check the machine and its network. Raptor can't manage it until it's back.",
				g.LastSeenAt.Time.UTC().Format("2 Jan 15:04 UTC")),
			Org: org, Node: g.NodeName, NodeID: node, URL: s.link(g.OrgID, node, ""), At: s.Now(),
		}
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			q := store.New(tx)
			if err := q.InsertAlertState(ctx, store.InsertAlertStateParams{RuleID: g.RuleID, Target: node, Since: g.LastSeenAt}); err != nil {
				return err
			}
			chans, err := q.ActiveAlertChannels(ctx, g.ChannelIds)
			if err != nil {
				return err
			}
			return s.queue(ctx, q, g.OrgID, g.RuleID, chans, m)
		})
		if err != nil {
			return err
		}
	}
	back, err := q.NodesBackOnline(ctx)
	if err != nil {
		return err
	}
	for _, b := range back {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			q := store.New(tx)
			if err := q.DeleteAlertState(ctx, store.DeleteAlertStateParams{RuleID: b.RuleID, Target: b.Target}); err != nil {
				return err
			}
			if !b.Enabled || b.NodeRemoved || !b.NotifyResolved {
				return nil
			}
			org, _ := q.OrgName(ctx, b.OrgID)
			m := Message{
				Condition: "node_offline", Level: Info, Type: "node.online", Title: "Node back online", Resolved: true,
				Text: fmt.Sprintf("Offline for %s.", s.Now().Sub(b.Since.Time).Round(time.Minute)),
				Org:  org, Node: b.NodeName, NodeID: b.Target, URL: s.link(b.OrgID, b.Target, ""), At: s.Now(),
			}
			chans, err := q.ActiveAlertChannels(ctx, b.ChannelIds)
			if err != nil {
				return err
			}
			return s.queue(ctx, q, b.OrgID, b.RuleID, chans, m)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Deliver sends what's due, a goroutine per channel so a slow one doesn't
// hold up the rest.
func (s *Service) Deliver(ctx context.Context) error {
	s.init()
	rows, err := store.New(s.DB).DueAlertDeliveries(ctx, 200)
	if err != nil {
		return err
	}
	byChannel := map[pgtype.UUID][]store.DueAlertDeliveriesRow{}
	var order []pgtype.UUID
	for _, r := range rows {
		if _, ok := byChannel[r.ChannelID]; !ok {
			order = append(order, r.ChannelID)
		}
		byChannel[r.ChannelID] = append(byChannel[r.ChannelID], r)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, c := range order {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			for _, r := range byChannel[c] {
				if ctx.Err() != nil {
					return
				}
				s.deliver(ctx, r)
			}
		})
	}
	wg.Wait()
	return nil
}

// allow applies the per-channel rate limit; it returns how many alerts were
// dropped since the last one sent.
func (s *Service) allow(c pgtype.UUID) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.limits[c]
	if l == nil {
		l = &limiter{}
		s.limits[c] = l
	}
	now := s.Now()
	if now.Sub(l.window) >= time.Minute {
		l.window, l.sent = now, 0
	}
	if l.sent >= perMinute {
		l.suppressed++
		return 0, false
	}
	l.sent++
	n := l.suppressed
	l.suppressed = 0
	return n, true
}

func (s *Service) deliver(ctx context.Context, r store.DueAlertDeliveriesRow) {
	q := store.New(s.DB)
	done := func(status, msg string, tried int32) {
		if err := q.AlertDeliveryDone(context.WithoutCancel(ctx), store.AlertDeliveryDoneParams{ID: r.ID, Status: status, Error: msg, Tried: tried}); err != nil {
			s.Log.Error("alerts: recording a delivery failed", "err", err)
		}
	}
	if r.DisabledAt.Valid {
		done("dropped", "the channel is turned off", 0)
		return
	}
	suppressed, ok := s.allow(r.ChannelID)
	if !ok {
		done("dropped", fmt.Sprintf("more than %d alerts a minute to this channel", perMinute), 0)
		return
	}
	var m Message
	if err := json.Unmarshal(r.Message, &m); err != nil {
		done("failed", "unreadable alert", 1)
		return
	}
	if suppressed > 0 {
		m.Text += fmt.Sprintf("\n\n(%d earlier alerts to this channel weren't sent: more than %d a minute.)", suppressed, perMinute)
	}
	t, err := s.target(ctx, r.OrgID, r.Type, r.Config, r.Secret)
	if err == nil {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = s.send.send(sctx, t, m)
		cancel()
	}
	if ctx.Err() != nil {
		return // shutting down: it's still pending
	}
	if err == nil {
		if err := q.AlertDeliverySent(ctx, r.ID); err != nil {
			s.Log.Error("alerts: recording a delivery failed", "err", err)
		}
		_ = q.AlertChannelWorked(ctx, r.ChannelID)
		return
	}
	var perm permanent
	if !errors.As(err, &perm) && int(r.Attempts)+1 < attempts {
		_ = q.AlertDeliveryRetry(ctx, store.AlertDeliveryRetryParams{
			ID: r.ID, NextAt: pgtype.Timestamptz{Time: s.Now().Add(retryAfter[r.Attempts]), Valid: true}, Error: err.Error(),
		})
		return
	}
	done("failed", err.Error(), 1)
	s.failed(ctx, r.ChannelID, err)
}

// failed records a channel that failed an alert for good, and turns it off
// once it's been failing for failingFor, telling the org's owners.
func (s *Service) failed(ctx context.Context, channel pgtype.UUID, cause error) {
	q := store.New(s.DB)
	c, err := q.AlertChannelFailed(ctx, store.AlertChannelFailedParams{ID: channel, LastError: cause.Error()})
	if err != nil || c.DisabledAt.Valid || s.Now().Sub(c.FailingSince.Time) < failingFor {
		return
	}
	if err := q.DisableAlertChannel(ctx, channel); err != nil {
		return
	}
	s.Log.Info("alerts: turned off a channel that kept failing", "channel", uuid.UUID(channel.Bytes).String(), "err", cause)
	if s.Mail == nil {
		return
	}
	org, _ := q.OrgName(ctx, c.OrgID)
	owners, _ := q.OrgOwnerEmails(ctx, c.OrgID)
	body := fmt.Sprintf("The alert channel %q in %s has failed every alert for 3 days, so it's turned off.\n\nThe last error: %s\n\nFix it and turn it back on in the org's settings, under Alerts:\n%s\n",
		c.Name, org, cause, s.link(c.OrgID, "", ""))
	for _, to := range owners {
		if err := s.Mail(ctx, to, "Raptor: an alert channel was turned off", body); err != nil {
			s.Log.Warn("alerts: emailing an owner failed", "err", err)
		}
	}
}

// target opens a channel for sending.
func (s *Service) target(ctx context.Context, org pgtype.UUID, typ string, rawConfig, sealed []byte) (target, error) {
	t := target{Type: typ}
	if err := json.Unmarshal(rawConfig, &t.Config); err != nil {
		return t, permanent{errors.New("unreadable channel")}
	}
	if len(sealed) > 0 {
		sec, err := s.open(org, sealed)
		if err != nil {
			return t, permanent{err}
		}
		t.Secret = sec
	}
	if typ == Email {
		var roles []string
		var users []pgtype.UUID
		switch t.Config.To {
		case "owners":
			roles = []string{"owner"}
		case "admins":
			roles = []string{"owner", "admin"}
		default:
			id, err := uuid.Parse(t.Config.To)
			if err != nil {
				return t, permanent{errors.New("unreadable channel")}
			}
			users = []pgtype.UUID{{Bytes: id, Valid: true}}
		}
		emails, err := store.New(s.DB).AlertRecipients(ctx, store.AlertRecipientsParams{OrgID: org, Roles: roles, Users: users})
		if err != nil {
			return t, err
		}
		t.Emails = emails
	}
	return t, nil
}

func (s *Service) aead() (cipher.AEAD, error) {
	if len(s.DataKey) != 32 {
		return nil, errors.New("this Panel can't keep secrets (PANEL_DATA_KEY isn't set)")
	}
	block, err := aes.NewCipher(s.DataKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aad binds a sealed secret to its org, so one copied to another org's
// channel doesn't open.
func aad(org pgtype.UUID) []byte {
	return append([]byte("raptor alert channel v1\x00"), org.Bytes[:]...)
}

func (s *Service) seal(org pgtype.UUID, sec secret) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	pt, err := json.Marshal(sec) //nolint:gosec // it's sealed right after
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(append([]byte{1}, nonce...), nonce, pt, aad(org)), nil
}

func (s *Service) open(org pgtype.UUID, sealed []byte) (secret, error) {
	var sec secret
	a, err := s.aead()
	if err != nil {
		return sec, err
	}
	n := 1 + a.NonceSize()
	if len(sealed) < n || sealed[0] != 1 {
		return sec, errors.New("unknown sealed channel format")
	}
	pt, err := a.Open(nil, sealed[1:n], sealed[n:], aad(org))
	if err != nil {
		return sec, errors.New("the channel's secret can't be opened")
	}
	return sec, json.Unmarshal(pt, &sec)
}

// Prepared is a checked channel, ready to store.
type Prepared struct {
	Config []byte
	Secret []byte // nil: none
	Hint   string
}

// Prepare checks a channel an admin entered and seals its secret.
func (s *Service) Prepare(org pgtype.UUID, in ChannelInput) (Prepared, error) {
	s.init()
	c, sec, hint, err := prepare(in, s.AllowPrivate)
	if err != nil {
		return Prepared{}, err
	}
	cfg, err := json.Marshal(c)
	if err != nil {
		return Prepared{}, err
	}
	out := Prepared{Config: cfg, Hint: hint}
	if sec != (secret{}) {
		if out.Secret, err = s.seal(org, sec); err != nil {
			return Prepared{}, err
		}
	}
	return out, nil
}

// Test sends a test alert to one channel now, without retries.
func (s *Service) Test(ctx context.Context, org pgtype.UUID, ch store.AlertChannel, orgName string) error {
	s.init()
	t, err := s.target(ctx, org, ch.Type, ch.Config, ch.Secret)
	if err != nil {
		return err
	}
	m := Message{
		Condition: "test", Level: Info, Type: "test", Title: "Test alert", Org: orgName, At: s.Now(),
		Text: "Alerts from Raptor reach this channel.", URL: s.link(org, "", ""),
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.send.send(sctx, t, m); err != nil {
		return err
	}
	_ = store.New(s.DB).AlertChannelWorked(ctx, ch.ID)
	return nil
}

// TelegramLink makes a Connect Telegram link for an org: opening it starts
// a chat with the bot (or adds it to a group), which then becomes a channel.
func (s *Service) TelegramLink(ctx context.Context, org, user pgtype.UUID) (chat, group string, expires time.Time, err error) {
	if s.Telegram == nil || s.Telegram.Username() == "" {
		return "", "", time.Time{}, errors.New("this Panel doesn't have a Telegram bot")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires = s.Now().Add(TelegramLinkTTL)
	if err := store.New(s.DB).InsertTelegramLink(ctx, store.InsertTelegramLinkParams{
		TokenHash: hash[:], OrgID: org, UserID: user, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", "", time.Time{}, err
	}
	return s.Telegram.LinkURL(token, false), s.Telegram.LinkURL(token, true), expires, nil
}

// TelegramStart adds the chat a Connect Telegram link was opened in as a
// channel, and says so.
func (s *Service) TelegramStart(ctx context.Context, chatID int64, title, token string) string {
	s.init()
	hash := sha256.Sum256([]byte(token))
	var org string
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		l, err := q.UseTelegramLink(ctx, hash[:])
		if err != nil {
			return err
		}
		if n, err := q.CountOrgAlertChannels(ctx, l.OrgID); err != nil || n >= ChannelsPerOrg {
			if err == nil {
				err = InputError{"limit"}
			}
			return err
		}
		cfg, _ := json.Marshal(config{ChatID: chatID, ChatTitle: title})
		name := "Telegram"
		if title != "" {
			name += ": " + title
		}
		id, err := q.InsertAlertChannel(ctx, store.InsertAlertChannelParams{
			OrgID: l.OrgID, Type: Telegram, Name: truncate(name, 60), Config: cfg, CreatedBy: l.UserID,
		})
		if err != nil {
			return err
		}
		if err := q.SetTelegramLinkChannel(ctx, store.SetTelegramLinkChannelParams{TokenHash: hash[:], ChannelID: id}); err != nil {
			return err
		}
		org, err = q.OrgName(ctx, l.OrgID)
		return err
	})
	var ie InputError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "That link has expired or was already used. Make a new one in Raptor: the org's settings, Alerts, Connect Telegram."
	case errors.As(err, &ie):
		return fmt.Sprintf("Your org already has %d alert channels, the most it can have. Remove one in Raptor first.", ChannelsPerOrg)
	case err != nil:
		s.Log.Error("alerts: connecting a Telegram chat failed", "err", err)
		return "Something went wrong connecting this chat. Try again in a minute."
	}
	return fmt.Sprintf("Connected. This chat can now get alerts for %s: pick it in the org's alert rules in Raptor.", org)
}

// nodeRule is a threshold rule as a node evaluates it.
type nodeRule struct {
	ID         string  `json:"id"`
	Condition  string  `json:"condition"`
	ServerID   string  `json:"server_id,omitempty"`
	Threshold  float64 `json:"threshold"`
	ForSeconds int     `json:"for_seconds"`
}

// SyncThresholds sends each connected node the threshold rules it should
// evaluate, when they've changed since it last accepted them (or its
// Wings version has, since an older one may not have known node.alerts).
func (s *Service) SyncThresholds(ctx context.Context) {
	s.init()
	if s.Nodes == nil || s.PanelKey == nil {
		return
	}
	q := store.New(s.DB)
	nodes, err := q.ConnectedNodesForAlerts(ctx)
	if err != nil {
		s.Log.Error("alerts: listing nodes failed", "err", err)
		return
	}
	for _, n := range nodes {
		if err := s.syncNode(ctx, q, n); err != nil && ctx.Err() == nil {
			s.Log.Info("alerts: couldn't send a node its rules; trying again later", "node", uuid.UUID(n.ID.Bytes).String(), "err", err)
		}
	}
}

func (s *Service) syncNode(ctx context.Context, q *store.Queries, n store.ConnectedNodesForAlertsRow) error {
	rows, err := q.NodeThresholdRules(ctx, store.NodeThresholdRulesParams{ID: n.ID, Conditions: ThresholdConditions()})
	if err != nil {
		return err
	}
	rules := make([]nodeRule, 0, len(rows))
	for _, r := range rows {
		rules = append(rules, nodeRule{
			ID: uuid.UUID(r.ID.Bytes).String(), Condition: r.Condition, ServerID: r.ServerID,
			Threshold: r.Threshold, ForSeconds: int(r.ForMinutes) * 60,
		})
	}
	body, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(append([]byte(n.WingsVersion+"\x00"), body...))
	hash := hex.EncodeToString(sum[:])
	if hash == n.AlertsHash || (len(rules) == 0 && n.AlertsHash == "") {
		return nil
	}
	node := uuid.UUID(n.ID.Bytes).String()
	env, err := nodecmd.New(s.PanelKey, node, UserID, "node.alerts", "", map[string]any{"rules": rules}, time.Minute)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.Nodes.Execute(cctx, node, raw)
	if err != nil {
		return err
	}
	// Recorded even when the node refused (an older Wings), so it isn't
	// asked again until the rules or its version change.
	return q.SetNodeAlerts(ctx, store.SetNodeAlertsParams{ID: n.ID, AlertsHash: hash, AlertsError: res.GetError()})
}
