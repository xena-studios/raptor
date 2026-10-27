package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/alerts"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// hookServer records what's posted to it, answering with status.
type hookServer struct {
	mu     sync.Mutex
	status int
	got    []*http.Request
	bodies [][]byte
}

func (h *hookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got, h.bodies = append(h.got, r), append(h.bodies, b)
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
}

func (h *hookServer) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.got)
}

// fakeTelegram is the Bot API: getMe, setWebhook, and sendMessage.
type fakeTelegram struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case strings.HasSuffix(r.URL.Path, "/getMe"):
		_, _ = io.WriteString(w, `{"ok":true,"result":{"username":"RaptorAlertsBot"}}`)
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		f.mu.Lock()
		f.sent = append(f.sent, body)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	default:
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}
}

// Alerts: new orgs start with their owners told, and everyone has their own
// email channel; admins manage team channels and rules, members can't see
// them; node events become alerts on the rules that match, sent to their
// people (in their inbox and on their own channels; members only about what
// they can see) and team channels; nodes going offline and coming back
// alert; threshold rules go to nodes; Telegram chats connect with a link,
// for an org or for someone's own alerts; channels that keep failing are
// turned off.
func TestAlerts(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	node := &fakeNode{t: t, panelKey: pub}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	tg := &fakeTelegram{}
	tgAPI := httptest.NewServer(tg)
	defer tgAPI.Close()
	now := time.Now()
	var nowMu sync.Mutex
	clock := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}
	dataKey := make([]byte, 32)
	_, _ = rand.Read(dataKey)
	svc := &alerts.Service{
		DB: db, DataKey: dataKey, Mail: mail.Send, AppURL: appOrigin, Nodes: node, PanelKey: key,
		AllowPrivate: true, Now: clock,
	}
	bot := &alerts.TelegramBot{Token: "123:abc", WebhookSecret: strings.Repeat("s", 32), API: tgAPI.URL, OnStart: svc.TelegramStart}
	if err := bot.Setup(ctx, "https://api.example/alerts/telegram/webhook"); err != nil {
		t.Fatal(err)
	}
	svc.Telegram = bot
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Alerts: &orgs.Alerts{Orgs: orgSvc, Alerts: svc}, AppOrigin: appOrigin}))
	defer srv.Close()
	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: email, Code: code}}})
		if err != nil {
			t.Fatal(err)
		}
		return b, res.GetUser().GetId()
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	carol, carolID := signUp("carol@example.com")
	res, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	acme := res.GetOrg().GetId()
	exec("INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", acme, bobID)
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key, last_seen_at) VALUES ($1, 'box-1', decode(repeat('00', 32), 'hex'), now()) RETURNING id`, acme).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'smp', 1, now(), now()), ($1, 's2', 'rust', 1, now(), now())`, nodeID)
	nodeUUID := pgtype.UUID{}
	_ = nodeUUID.Scan(nodeID)

	// The starting alerts: the owners are told, by their own email.
	chans, err := alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{OrgId: acme})
	if err != nil || len(chans.GetChannels()) != 0 || len(chans.GetTypes()) != 5 || slices.Contains(chans.GetTypes(), "email") {
		t.Fatalf("starting channels: %v, %v", chans, err)
	}
	mine, err := alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{})
	if err != nil || len(mine.GetChannels()) != 1 || mine.GetChannels()[0].GetType() != "email" || mine.GetChannels()[0].GetTarget() != "alice@example.com" ||
		slices.Contains(mine.GetTypes(), "email") {
		t.Fatalf("alice's own channels: %v, %v", mine, err)
	}
	aliceEmail := mine.GetChannels()[0].GetId()
	if theirs, err := bob.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{}); err != nil || len(theirs.GetChannels()) != 1 || theirs.GetChannels()[0].GetId() == aliceEmail {
		t.Errorf("bob's own channels: %v, %v", theirs, err)
	}
	if _, err := bob.alerts.UpdateAlertChannel(ctx, &panelv1.UpdateAlertChannelRequest{Id: aliceEmail, Name: "mine"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("bob changing alice's channel: %v", err)
	}
	rules, err := alice.alerts.ListAlertRules(ctx, &panelv1.ListAlertRulesRequest{OrgId: acme})
	if err != nil || len(rules.GetRules()) != 3 || len(rules.GetConditions()) != len(alerts.Conditions) ||
		!slices.Equal(rules.GetRules()[0].GetNotifyRoles(), []string{"owner"}) || len(rules.GetRules()[0].GetChannelIds()) != 0 {
		t.Fatalf("starting rules: %v, %v", rules, err)
	}
	if _, err := bob.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{OrgId: acme}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member listing channels: %v", err)
	}
	if _, err := carol.alerts.ListAlertRules(ctx, &panelv1.ListAlertRulesRequest{OrgId: acme}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider listing rules: %v", err)
	}

	// Channels: checked, and their secrets never shown.
	for _, bad := range []*panelv1.CreateAlertChannelRequest{
		{Type: "discord", Url: "https://evil.example/api/webhooks/1/x"},
		{Type: "slack", Url: "https://hooks.slack.com.evil.example/services/x"},
		{Type: "webhook", Url: "ftp://example.com"},
		{Type: "ntfy", NtfyTopic: "has spaces"},
		{Type: "email"}, // email is people's own
		{Type: "telegram"},
		{Type: "sms"},
	} {
		bad.OrgId = acme
		if _, err := alice.alerts.CreateAlertChannel(ctx, bad); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s %v: %v", bad.GetType(), bad, err)
		}
	}
	if _, err := bob.alerts.CreateAlertChannel(ctx, &panelv1.CreateAlertChannelRequest{OrgId: acme, Type: "ntfy", NtfyTopic: "bobs-topic"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member adding a channel: %v", err)
	}
	discord, err := alice.alerts.CreateAlertChannel(ctx, &panelv1.CreateAlertChannelRequest{OrgId: acme, Type: "discord", Url: "https://discordapp.com/api/webhooks/123456789/tok-en_x"})
	if err != nil || strings.Contains(discord.GetChannel().GetTarget(), "tok-en") || discord.GetChannel().GetName() != "Discord" {
		t.Fatalf("discord: %v, %v", discord, err)
	}
	hook := &hookServer{}
	hooks := httptest.NewServer(hook)
	defer hooks.Close()
	wh, err := alice.alerts.CreateAlertChannel(ctx, &panelv1.CreateAlertChannelRequest{OrgId: acme, Type: "webhook", Name: "Ops", Url: hooks.URL + "/raptor", Secret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, "SELECT secret FROM alert_channels WHERE id = $1", wh.GetChannel().GetId()).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("s3cret")) {
		t.Errorf("the webhook's secret is stored in the clear: %v", err)
	}
	if _, err := alice.alerts.CreateAlertChannel(ctx, &panelv1.CreateAlertChannelRequest{Type: "email"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a second email channel of one's own: %v", err)
	}
	// Bob may see the rust server, not smp.
	exec("INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions) VALUES ($1, $2, $3, 's2', '{files.read}')", acme, bobID, nodeID)

	// A test alert, signed.
	tst, err := alice.alerts.TestAlertChannel(ctx, &panelv1.TestAlertChannelRequest{OrgId: acme, Id: wh.GetChannel().GetId()})
	if err != nil || !tst.GetOk() || hook.count() != 1 {
		t.Fatalf("test: %v, %v", tst, err)
	}
	h := hook.got[0]
	if want := "sha256=" + alerts.Sign("s3cret", h.Header.Get("X-Raptor-Timestamp"), hook.bodies[0]); h.Header.Get("X-Raptor-Signature") != want {
		t.Errorf("signature %q, want %q", h.Header.Get("X-Raptor-Signature"), want)
	}

	// Rules: checked.
	whID := wh.GetChannel().GetId()
	for _, bad := range []*panelv1.AlertRule{
		{Condition: "nope", ChannelIds: []string{whID}},
		{Condition: "crash"},
		{Condition: "crash", NotifyRoles: []string{"boss"}},
		{Condition: "cpu_high", Threshold: 150, ForMinutes: 5, ChannelIds: []string{whID}},
		{Condition: "cpu_high", Threshold: 90, ForMinutes: 0, ChannelIds: []string{whID}},
		{Condition: "node_disk_low", NodeId: nodeID, ServerId: "s1", ChannelIds: []string{whID}},
		{Condition: "crash", ServerId: "s1", ChannelIds: []string{whID}},
	} {
		if _, err := alice.alerts.SaveAlertRule(ctx, &panelv1.SaveAlertRuleRequest{OrgId: acme, Rule: bad}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("rule %v: %v", bad, err)
		}
	}
	if _, err := alice.alerts.SaveAlertRule(ctx, &panelv1.SaveAlertRuleRequest{OrgId: acme, Rule: &panelv1.AlertRule{Condition: "crash", NodeId: nodeID, ServerId: "nope", ChannelIds: []string{whID}, Enabled: true}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a rule for a server that doesn't exist: %v", err)
	}
	save := func(r *panelv1.AlertRule) string {
		t.Helper()
		r.Enabled = true
		res, err := alice.alerts.SaveAlertRule(ctx, &panelv1.SaveAlertRuleRequest{OrgId: acme, Rule: r})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetRule().GetId()
	}
	save(&panelv1.AlertRule{Name: "smp crashes", Condition: "crash", NodeId: nodeID, ServerId: "s1", ChannelIds: []string{whID}})
	if _, err := alice.alerts.SaveAlertRule(ctx, &panelv1.SaveAlertRuleRequest{OrgId: acme, Rule: &panelv1.AlertRule{Condition: "crash", NotifyUserIds: []string{carolID}}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("telling someone outside the org: %v", err)
	}
	save(&panelv1.AlertRule{Condition: "install_finished", NotifyUserIds: []string{bobID}})
	cpu := save(&panelv1.AlertRule{Condition: "cpu_high", Threshold: 90, ForMinutes: 5, ChannelIds: []string{whID}, NotifyResolved: true})

	// Events become alerts on the rules that match.
	ingest := func(evs ...*nodev1.Event) {
		t.Helper()
		if err := svc.Ingest(ctx, store.New(db), nodeUUID, evs); err != nil {
			t.Fatal(err)
		}
		if err := svc.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
	}
	at := clock().UnixMilli()
	data := func(v map[string]any) []byte { b, _ := json.Marshal(v); return b }
	ingest(
		&nodev1.Event{Type: "server.crashed", ServerId: "s2", At: at, Data: data(map[string]any{"exit_code": 1})}, // no rule for s2
		&nodev1.Event{Type: "server.crashed", ServerId: "s1", At: at, Data: data(map[string]any{"exit_code": 137, "reason": "oom", "console": []string{"boom"}})},
		&nodev1.Event{Type: "server.install.finished", ServerId: "s2", At: at},
		&nodev1.Event{Type: "server.install.finished", ServerId: "s1", At: at}, // bob can't see smp
		&nodev1.Event{Type: "server.state", ServerId: "s1", At: at},
		&nodev1.Event{Type: "server.crashed", ServerId: "s1", At: clock().Add(-7 * time.Hour).UnixMilli()}, // stale
	)
	if hook.count() != 2 {
		t.Fatalf("webhook got %d, want the test and one crash", hook.count())
	}
	var m alerts.Message
	_ = json.Unmarshal(hook.bodies[1], &m)
	if m.Condition != "crash" || m.Server != "smp" || m.Node != "box-1" || m.Org != "Acme" || m.Rule != "smp crashes" || !strings.Contains(m.Text, "boom") ||
		m.URL != appOrigin+"/orgs/"+acme[strings.LastIndexByte(acme, '-')+1:]+"/servers/s1" {
		t.Errorf("crash alert: %+v", m)
	}
	if got := mail.mail[len(mail.mail)-1]; !strings.HasPrefix(got, "bob@example.com\n") || !strings.Contains(got, "Install finished") || !strings.Contains(got, "Server: rust\nNode: box-1") {
		t.Errorf("install email: %q", got)
	}
	if got := mail.mail[len(mail.mail)-2]; strings.HasPrefix(got, "bob@example.com\n") {
		t.Errorf("bob was told about a server he can't see: %q", got)
	}
	// It's in bob's inbox too, once.
	box, err := bob.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{})
	if err != nil || len(box.GetNotifications()) != 1 || box.GetUnseen() != 1 || box.GetNotifications()[0].GetServerName() != "rust" ||
		box.GetNotifications()[0].GetOrgName() != "Acme" || box.GetNotifications()[0].GetSeen() {
		t.Fatalf("bob's inbox: %v, %v", box, err)
	}
	if box, _ := alice.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 0 {
		t.Errorf("alice's inbox has bob's alert: %v", box)
	}
	if _, err := bob.alerts.SeeNotifications(ctx, &panelv1.SeeNotificationsRequest{}); err != nil {
		t.Fatal(err)
	}
	if box, _ := bob.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); box.GetUnseen() != 0 || !box.GetNotifications()[0].GetSeen() {
		t.Errorf("seen: %v", box)
	}
	if _, err := alice.alerts.DismissNotifications(ctx, &panelv1.DismissNotificationsRequest{Ids: []string{box.GetNotifications()[0].GetId()}}); err != nil {
		t.Fatal(err)
	}
	if box, _ := bob.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 1 {
		t.Errorf("alice dismissed bob's alert: %v", box)
	}
	if _, err := bob.alerts.DismissNotifications(ctx, &panelv1.DismissNotificationsRequest{}); err != nil {
		t.Fatal(err)
	}
	if box, _ := bob.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 0 {
		t.Errorf("after dismissing everything: %v", box)
	}

	// A threshold firing on the node names its rule; resolving is sent too.
	ingest(&nodev1.Event{Type: "alert.fired", ServerId: "s2", At: at, Data: data(map[string]any{"rule_id": cpu, "condition": "cpu_high", "value": 97.2, "threshold": 90, "for_seconds": 300})},
		&nodev1.Event{Type: "alert.fired", ServerId: "s2", At: at, Data: data(map[string]any{"rule_id": "00000000-0000-0000-0000-000000000000", "condition": "cpu_high", "value": 99})})
	ingest(&nodev1.Event{Type: "alert.resolved", ServerId: "s2", At: at, Data: data(map[string]any{"rule_id": cpu, "condition": "cpu_high", "value": 40, "threshold": 90})})
	if hook.count() != 4 {
		t.Fatalf("webhook got %d after thresholds, want 4", hook.count())
	}
	_ = json.Unmarshal(hook.bodies[2], &m)
	if m.Title != "CPU above 90%" || !strings.Contains(m.Text, "97%") || m.Resolved {
		t.Errorf("fired: %+v", m)
	}
	_ = json.Unmarshal(hook.bodies[3], &m)
	if !m.Resolved || m.Level != alerts.Info {
		t.Errorf("resolved: %+v", m)
	}

	// Database instances: on the node's Databases page, and for admins
	// only, even when a rule names a member.
	if _, err := alice.alerts.SaveAlertRule(ctx, &panelv1.SaveAlertRuleRequest{OrgId: acme, Rule: &panelv1.AlertRule{
		Condition: "db_down", NodeId: nodeID, ServerId: "s1", ChannelIds: []string{whID},
	}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an instance rule for a server: %v", err)
	}
	save(&panelv1.AlertRule{Condition: "db_down", ChannelIds: []string{whID}, NotifyRoles: []string{"owner", "member"}, NotifyUserIds: []string{bobID}, NotifyResolved: true})
	dbConns := save(&panelv1.AlertRule{Condition: "db_connections_high", Threshold: 90, ForMinutes: 5, ChannelIds: []string{whID}})
	ingest(&nodev1.Event{Type: "db.instance.down", At: at, Data: data(map[string]any{
		"instance_id": "i1", "instance": "main", "engine": "postgres", "version": "17", "error": "it stopped (exit code 137)",
	})})
	if hook.count() != 5 {
		t.Fatalf("webhook got %d after an instance down, want 5", hook.count())
	}
	m = alerts.Message{}
	_ = json.Unmarshal(hook.bodies[4], &m)
	if m.Condition != "db_down" || m.Title != "Database instance down: main" || m.Server != "" ||
		m.URL != appOrigin+"/orgs/"+acme[strings.LastIndexByte(acme, '-')+1:]+"/nodes/"+nodeID[strings.LastIndexByte(nodeID, '-')+1:]+"/databases" {
		t.Errorf("instance down: %+v", m)
	}
	if box, _ := bob.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 0 {
		t.Errorf("a member was told about a database instance: %v", box)
	}
	if box, _ := alice.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 1 {
		t.Errorf("the owner wasn't told about a database instance: %v", box)
	}
	if _, err := alice.alerts.DismissNotifications(ctx, &panelv1.DismissNotificationsRequest{}); err != nil {
		t.Fatal(err)
	}

	// Threshold rules go to connected nodes, once; instance ones only to
	// nodes that know them.
	exec("INSERT INTO panel_instances (id) VALUES ('test')")
	exec("INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')", nodeID)
	svc.SyncThresholds(ctx)
	svc.SyncThresholds(ctx)
	node.mu.Lock()
	if len(node.got) != 1 || node.got[0].Action != "node.alerts" || !strings.Contains(string(node.got[0].Params), cpu) ||
		strings.Contains(string(node.got[0].Params), dbConns) {
		t.Errorf("node.alerts: %+v", node.got)
	}
	node.mu.Unlock()
	exec("UPDATE nodes SET capabilities = '{db-alerts}' WHERE id = $1", nodeID)
	svc.SyncThresholds(ctx)
	node.mu.Lock()
	if len(node.got) != 2 || !strings.Contains(string(node.got[1].Params), dbConns) {
		t.Errorf("node.alerts with db-alerts: %+v", node.got)
	}
	node.mu.Unlock()

	// Offline: after the grace, counted from when it was noticed too; back
	// online resolves it.
	exec("DELETE FROM node_connections")
	exec("UPDATE nodes SET last_seen_at = now() - interval '1 hour' WHERE id = $1", nodeID)
	offline := func() {
		t.Helper()
		if err := svc.Offline(ctx); err != nil {
			t.Fatal(err)
		}
		if err := svc.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
	}
	before := len(mail.mail)
	offline()
	if len(mail.mail) != before {
		t.Fatal("offline alerted before its grace counted from noticing it")
	}
	advance(6 * time.Minute)
	offline()
	offline()
	if len(mail.mail) != before+1 || !strings.Contains(mail.mail[before], "Node offline") || !strings.HasPrefix(mail.mail[before], "alice@example.com\n") {
		t.Fatalf("offline emails: %q", mail.mail[before:])
	}
	exec("UPDATE panel_instances SET seen_at = now()")
	exec("INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')", nodeID)
	offline()
	if len(mail.mail) != before+2 || !strings.Contains(mail.mail[before+1], "Node back online") {
		t.Fatalf("back online: %q", mail.mail[before:])
	}
	if box, _ := alice.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 2 ||
		box.GetNotifications()[0].GetTitle() != "Node back online" || box.GetNotifications()[1].GetNodeName() != "box-1" {
		t.Errorf("alice's inbox: %v", box)
	}
	// Turning her email off, she's only told in the app.
	if _, err := alice.alerts.UpdateAlertChannel(ctx, &panelv1.UpdateAlertChannelRequest{Id: aliceEmail, Name: "Email", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	exec("DELETE FROM node_connections")
	advance(10 * time.Minute)
	offline()
	advance(10 * time.Minute)
	offline()
	if len(mail.mail) != before+2 {
		t.Errorf("emailed with her email turned off: %q", mail.mail[before+2:])
	}
	if box, _ := alice.alerts.ListNotifications(ctx, &panelv1.ListNotificationsRequest{}); len(box.GetNotifications()) != 3 {
		t.Errorf("the inbox after: %v", box)
	}
	exec("INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')", nodeID)
	offline()

	// Telegram: a link, /start in the chat, and the chat is a channel.
	link, err := alice.alerts.ConnectTelegram(ctx, &panelv1.ConnectTelegramRequest{OrgId: acme})
	if err != nil || !strings.HasPrefix(link.GetChatUrl(), "https://t.me/RaptorAlertsBot?start=") {
		t.Fatalf("telegram link: %v, %v", link, err)
	}
	token := strings.TrimPrefix(link.GetChatUrl(), "https://t.me/RaptorAlertsBot?start=")
	update := func(secret, text string) int {
		t.Helper()
		body := `{"message":{"text":` + string(data(map[string]any{"t": text})[5:len(data(map[string]any{"t": text}))-1]) + `,"chat":{"id":-1001,"type":"group","title":"Ops chat"}}}`
		req := httptest.NewRequest(http.MethodPost, alerts.TelegramPath, strings.NewReader(body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		w := httptest.NewRecorder()
		bot.ServeHTTP(w, req)
		return w.Code
	}
	if code := update("wrong", "/start "+token); code != http.StatusForbidden {
		t.Errorf("an update without the secret: %d", code)
	}
	if code := update(bot.WebhookSecret, "/start@RaptorAlertsBot "+token); code != http.StatusOK {
		t.Fatalf("start: %d", code)
	}
	update(bot.WebhookSecret, "/start "+token) // used up
	tg.mu.Lock()
	if len(tg.sent) != 2 || !strings.Contains(tg.sent[0]["text"].(string), "Connected") || !strings.Contains(tg.sent[1]["text"].(string), "expired") {
		t.Errorf("bot replies: %v", tg.sent)
	}
	tg.mu.Unlock()
	chans, _ = alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{OrgId: acme})
	var tgID string
	for _, c := range chans.GetChannels() {
		if c.GetType() == "telegram" {
			tgID = c.GetId()
			if c.GetTarget() != "Ops chat" {
				t.Errorf("telegram channel: %v", c)
			}
		}
	}
	if tst, err := alice.alerts.TestAlertChannel(ctx, &panelv1.TestAlertChannelRequest{OrgId: acme, Id: tgID}); err != nil || !tst.GetOk() {
		t.Fatalf("telegram test: %v, %v", tst, err)
	}
	// And a chat for bob's own alerts.
	link, err = bob.alerts.ConnectTelegram(ctx, &panelv1.ConnectTelegramRequest{})
	if err != nil {
		t.Fatal(err)
	}
	update(bot.WebhookSecret, "/start "+strings.TrimPrefix(link.GetChatUrl(), "https://t.me/RaptorAlertsBot?start="))
	tg.mu.Lock()
	if last := tg.sent[len(tg.sent)-1]["text"].(string); !strings.Contains(last, "Your alerts") {
		t.Errorf("bot reply to a personal link: %q", last)
	}
	tg.mu.Unlock()
	if theirs, _ := bob.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{}); len(theirs.GetChannels()) != 2 || theirs.GetChannels()[1].GetType() != "telegram" {
		t.Errorf("bob's own channels after Telegram: %v", theirs)
	}
	if chans, _ := alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{OrgId: acme}); len(chans.GetChannels()) != 3 {
		t.Errorf("bob's chat became an org channel: %v", chans)
	}

	// A channel that fails: retried, then failed; after 3 days of it, off,
	// and the owners told.
	hook.mu.Lock()
	hook.status = http.StatusBadGateway
	hook.mu.Unlock()
	ingest(&nodev1.Event{Type: "server.crashed", ServerId: "s1", At: clock().UnixMilli()})
	var status string
	var attempts int
	_ = db.QueryRow(ctx, "SELECT status, attempts FROM alert_deliveries WHERE channel_id = $1 ORDER BY created_at DESC LIMIT 1", whID).Scan(&status, &attempts)
	if status != "pending" || attempts != 1 {
		t.Errorf("after a 502: %s, %d tries", status, attempts)
	}
	hook.mu.Lock()
	hook.status = http.StatusNotFound
	hook.mu.Unlock()
	exec("UPDATE alert_deliveries SET next_at = now()")
	if err := svc.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(ctx, "SELECT status FROM alert_deliveries WHERE channel_id = $1 ORDER BY created_at DESC LIMIT 1", whID).Scan(&status)
	if status != "failed" {
		t.Errorf("after a 404: %s", status)
	}
	exec("UPDATE alert_channels SET failing_since = now() - interval '4 days' WHERE id = $1", whID)
	before = len(mail.mail)
	ingest(&nodev1.Event{Type: "server.crashed", ServerId: "s1", At: clock().UnixMilli()})
	chans, _ = alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{OrgId: acme})
	for _, c := range chans.GetChannels() {
		if c.GetId() == whID && (c.GetEnabled() || !strings.Contains(c.GetError(), "404")) {
			t.Errorf("a channel failing for 4 days: %v", c)
		}
	}
	if len(mail.mail) != before+1 || !strings.Contains(mail.mail[before], "turned off") {
		t.Errorf("owners not told: %q", mail.mail[before:])
	}
	// Turned back on, it works again.
	hook.mu.Lock()
	hook.status = 0
	hook.mu.Unlock()
	if _, err := alice.alerts.UpdateAlertChannel(ctx, &panelv1.UpdateAlertChannelRequest{OrgId: acme, Id: whID, Name: "Ops", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	n := hook.count()
	ingest(&nodev1.Event{Type: "server.crashed", ServerId: "s1", At: clock().UnixMilli()})
	if hook.count() != n+1 {
		t.Error("a channel turned back on doesn't get alerts")
	}

	// History, and deleting a channel takes it out of its rules.
	hist, err := alice.alerts.ListAlertHistory(ctx, &panelv1.ListAlertHistoryRequest{OrgId: acme})
	if err != nil || len(hist.GetEntries()) < 8 || hist.GetEntries()[0].GetServerName() != "smp" {
		t.Errorf("history: %d entries, %v", len(hist.GetEntries()), err)
	}
	if _, err := alice.alerts.DeleteAlertChannel(ctx, &panelv1.DeleteAlertChannelRequest{OrgId: acme, Id: whID}); err != nil {
		t.Fatal(err)
	}
	rules, _ = alice.alerts.ListAlertRules(ctx, &panelv1.ListAlertRulesRequest{OrgId: acme})
	for _, r := range rules.GetRules() {
		for _, c := range r.GetChannelIds() {
			if c == whID {
				t.Errorf("rule %s still sends to a deleted channel", r.GetId())
			}
		}
	}
	if _, err := alice.alerts.DeleteAlertChannel(ctx, &panelv1.DeleteAlertChannelRequest{Id: aliceEmail}); err != nil {
		t.Fatal(err)
	}
	if mine, _ := alice.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{}); len(mine.GetChannels()) != 0 || !slices.Contains(mine.GetTypes(), "email") {
		t.Errorf("after removing her email: %v", mine)
	}
}

// Channels never reach private addresses unless allowed (development).
func TestAlertsPrivateAddresses(t *testing.T) {
	hook := &hookServer{}
	hooks := httptest.NewServer(hook)
	defer hooks.Close()
	db := paneltest.NewDB(t)
	dataKey := make([]byte, 32)
	svc := &alerts.Service{DB: db, DataKey: dataKey}
	var org pgtype.UUID
	if err := db.QueryRow(context.Background(), "INSERT INTO orgs (name) VALUES ('x') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Prepare(alerts.Owner{Org: org}, alerts.ChannelInput{Type: "webhook", URL: hooks.URL}); err == nil {
		t.Error("an http:// webhook was accepted")
	}
	p, err := svc.Prepare(alerts.Owner{Org: org}, alerts.ChannelInput{Type: "webhook", URL: strings.Replace(hooks.URL, "http://", "https://", 1)})
	if err != nil {
		t.Fatal(err)
	}
	ch := store.AlertChannel{OrgID: org, Type: "webhook", Config: p.Config, Secret: p.Secret}
	if err := svc.Test(context.Background(), alerts.Owner{Org: org}, ch, "x"); err == nil || !strings.Contains(err.Error(), "public internet") {
		t.Errorf("a webhook to 127.0.0.1: %v", err)
	}
	if hook.count() != 0 {
		t.Error("the private address was reached")
	}
}
