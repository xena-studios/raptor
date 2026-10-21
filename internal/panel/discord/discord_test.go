package discord

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

const (
	guild    = "100"
	customer = "200"
)

// fakeDiscord is Discord's API: the server's members, and the role changes
// and command updates the bot makes.
type fakeDiscord struct {
	mu      sync.Mutex
	members []member
	calls   []string
	// bodies are what was sent with each call, by "METHOD path".
	bodies map[string]string
	// archived is whether ticket threads read as archived.
	archived bool
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bot token" {
		http.Error(w, `{"message":"401: Unauthorized","code":0}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/guilds/"+guild+"/members":
		after := r.URL.Query().Get("after")
		var page []member
		for _, m := range f.members {
			if a, _ := strconv.ParseUint(after, 10, 64); mustUint(m.User.ID) > a {
				page = append(page, m)
			}
		}
		_ = json.NewEncoder(w).Encode(page)
		return
	case strings.HasPrefix(r.URL.Path, "/guilds/"+guild+"/members/"):
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPut && r.URL.Path == "/applications/1/guilds/"+guild+"/commands":
		f.calls = append(f.calls, "commands")
		_, _ = w.Write([]byte("[]"))
		return
	case r.Method == http.MethodGet && r.URL.Path == "/channels/777":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "777", "thread_metadata": map[string]any{"archived": f.archived}})
		return
	case strings.HasPrefix(r.URL.Path, "/channels/") || strings.HasPrefix(r.URL.Path, "/webhooks/"):
		call := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, call)
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		if f.bodies == nil {
			f.bodies = map[string]string{}
		}
		f.bodies[call] = body.String()
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"id":"777"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, `{"message":"Unknown","code":10000}`, http.StatusNotFound)
}

func (f *fakeDiscord) body(call string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[call]
}

func (f *fakeDiscord) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.calls)
	f.calls = nil
	return out
}

func mustUint(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// fixture is a database with:
//   - Discord 1 linked to an owner of an org with a plan (a customer),
//   - Discord 2 linked to a member of that org (not a customer),
//   - Discord 3 linked to the owner of an org whose plan ended,
//   - Discord 4 not linked.
func fixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := paneltest.NewDB(t)
	_, err := db.Exec(context.Background(), `
		INSERT INTO users (id, email) VALUES
			('00000000-0000-0000-0000-000000000001', 'owner@example.com'),
			('00000000-0000-0000-0000-000000000002', 'member@example.com'),
			('00000000-0000-0000-0000-000000000003', 'lapsed@example.com');
		INSERT INTO orgs (id, name) VALUES
			('00000000-0000-0000-0000-0000000000a1', 'Paid'),
			('00000000-0000-0000-0000-0000000000a2', 'Lapsed');
		INSERT INTO org_members (org_id, user_id, role) VALUES
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000001', 'owner'),
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000002', 'member'),
			('00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-000000000003', 'owner');
		INSERT INTO billing_accounts (org_id, subscription_id, status) VALUES
			('00000000-0000-0000-0000-0000000000a1', 'sub_1', 'active'),
			('00000000-0000-0000-0000-0000000000a2', 'sub_2', 'canceled');
		INSERT INTO oauth_accounts (user_id, provider, subject) VALUES
			('00000000-0000-0000-0000-000000000001', 'discord', '1'),
			('00000000-0000-0000-0000-000000000002', 'discord', '2'),
			('00000000-0000-0000-0000-000000000003', 'discord', '3');
		INSERT INTO nodes (org_id, name, public_key, wings_version) VALUES
			('00000000-0000-0000-0000-0000000000a1', 'box', decode(repeat('00', 32), 'hex'), 'v0.9.0');
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func newBot(t *testing.T, db *pgxpool.Pool, f *fakeDiscord) (*Bot, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	return &Bot{
		Token: "token", AppID: "1", PublicKey: pub, GuildID: guild, CustomerRole: customer,
		Billing: true, AppURL: "https://app.example", DB: db, API: api.URL,
	}, priv
}

func TestSync(t *testing.T) {
	f := &fakeDiscord{members: []member{
		{User: user{ID: "1"}},                                       // a customer without the role: added
		{User: user{ID: "2"}, Roles: []string{customer}},            // a member with it: removed
		{User: user{ID: "3"}, Roles: []string{customer}},            // a lapsed plan: removed
		{User: user{ID: "4"}},                                       // not linked: left alone
		{User: user{ID: "5", Bot: true}, Roles: []string{customer}}, // bots: left alone
	}}
	b, _ := newBot(t, fixture(t), f)
	ran, err := b.Sync(context.Background())
	if err != nil || !ran {
		t.Fatalf("Sync: %v, %v", ran, err)
	}
	want := []string{
		"PUT /guilds/" + guild + "/members/1/roles/" + customer,
		"DELETE /guilds/" + guild + "/members/2/roles/" + customer,
		"DELETE /guilds/" + guild + "/members/3/roles/" + customer,
	}
	if got := f.took(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}

	// Without billing, anyone running a node is a customer.
	b.Billing = false
	f.members = []member{{User: user{ID: "1"}}, {User: user{ID: "3"}}}
	if _, err := b.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.took(); !slices.Equal(got, want[:1]) {
		t.Fatalf("calls without billing = %v, want %v", got, want[:1])
	}
}

// signed posts an interaction to the bot, signed as Discord would.
func signed(t *testing.T, b *Bot, key ed25519.PrivateKey, body any, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(at.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, InteractionsPath, bytes.NewReader(data))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(ed25519.Sign(key, append([]byte(ts), data...))))
	w := httptest.NewRecorder()
	b.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestInteractionSignatures(t *testing.T) {
	b, key := newBot(t, nil, &fakeDiscord{})
	ping := map[string]any{"type": 1}

	if w, out := signed(t, b, key, ping, time.Now()); w.Code != http.StatusOK || out["type"] != float64(1) {
		t.Fatalf("a signed ping: %d %v", w.Code, out)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	if w, _ := signed(t, b, other, ping, time.Now()); w.Code != http.StatusUnauthorized {
		t.Fatalf("someone else's signature: %d, want 401", w.Code)
	}
	if w, _ := signed(t, b, key, ping, time.Now().Add(-time.Hour)); w.Code != http.StatusUnauthorized {
		t.Fatalf("an old interaction: %d, want 401", w.Code)
	}
	req := httptest.NewRequest(http.MethodPost, InteractionsPath, strings.NewReader(`{"type":1}`))
	w := httptest.NewRecorder()
	b.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d, want 401", w.Code)
	}
}

func command(name, from string, roles []string, options ...map[string]any) map[string]any {
	return map[string]any{
		"type": 2, "guild_id": guild,
		"member": map[string]any{"user": map[string]any{"id": from, "username": "u" + from}, "roles": roles},
		"data":   map[string]any{"name": name, "options": options},
	}
}

// content is a reply's text and its embeds' descriptions, together.
func content(out map[string]any) string {
	data, _ := json.Marshal(out["data"])
	return string(data)
}

func TestLink(t *testing.T) {
	f := &fakeDiscord{}
	b, key := newBot(t, fixture(t), f)

	_, out := signed(t, b, key, command("link", "4", nil), time.Now())
	if s := content(out); !strings.Contains(s, "https://app.example/settings?connect=discord") || !strings.Contains(s, `"flags":64`) {
		t.Fatalf("unlinked /link = %s", s)
	}
	if got := f.took(); len(got) != 0 {
		t.Fatalf("unlinked /link changed roles: %v", got)
	}

	// A linked customer missing the role gets it on the spot.
	_, out = signed(t, b, key, command("link", "1", nil), time.Now())
	if s := content(out); !strings.Contains(s, "owner@example.com") || !strings.Contains(s, "You have the **Customer** role") {
		t.Fatalf("customer /link = %s", s)
	}
	if got := f.took(); !slices.Equal(got, []string{"PUT /guilds/" + guild + "/members/1/roles/" + customer}) {
		t.Fatalf("customer /link calls = %v", got)
	}

	_, out = signed(t, b, key, command("link", "2", []string{customer}), time.Now())
	if s := content(out); !strings.Contains(s, "No **Customer** role") {
		t.Fatalf("member /link = %s", s)
	}
	if got := f.took(); !slices.Equal(got, []string{"DELETE /guilds/" + guild + "/members/2/roles/" + customer}) {
		t.Fatalf("member /link calls = %v", got)
	}
}

func TestWhois(t *testing.T) {
	db := fixture(t)
	b, key := newBot(t, db, &fakeDiscord{})
	ask := func(from string) string {
		_, out := signed(t, b, key, command("whois", from, nil, map[string]any{"name": "member", "type": 6, "value": "1"}), time.Now())
		return content(out)
	}
	if s := ask("2"); !strings.Contains(s, "Only Raptor staff") {
		t.Fatalf("/whois from someone who isn't staff = %s", s)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO staff (user_id, role, added_by) VALUES ('00000000-0000-0000-0000-000000000002', 'support', 'test')`); err != nil {
		t.Fatal(err)
	}
	s := ask("2")
	for _, want := range []string{"owner@example.com", "Paid", "Owner · paid", "1 node · Wings v0.9.0", "/admin/users/00000000-0000-0000-0000-000000000001", `"flags":64`} {
		if !strings.Contains(s, want) {
			t.Fatalf("/whois = %s, want %q in it", s, want)
		}
	}
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM staff_audit WHERE action = 'user.whois' AND staff_email = 'member@example.com'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit entries = %d, %v; want 1", n, err)
	}
}

func TestDocs(t *testing.T) {
	pages := []Page{
		{Title: "Link a node", Section: "Install", Path: "/install/link-a-node/", Description: "Run the installer."},
		{Title: "Backups", Section: "Servers", Path: "/servers/backups/", Description: "Schedules and restores."},
		{Title: "From Pterodactyl", Section: "Install", Path: "/install/from-pterodactyl/", Description: "Move your servers and backups over."},
	}
	got := search(pages, "backups")
	if len(got) != 2 || got[0].Path != "/servers/backups/" {
		t.Fatalf("search(backups) = %v; the title match should come first", got)
	}
	if got := search(pages, "install ptero"); len(got) != 1 || got[0].Path != "/install/from-pterodactyl/" {
		t.Fatalf("search(install ptero) = %v", got)
	}
	if got := search(pages, "nothing like it"); len(got) != 0 {
		t.Fatalf("search(nothing like it) = %v", got)
	}

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DocsIndexPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(pages)
	}))
	defer docs.Close()
	b, key := newBot(t, nil, &fakeDiscord{})
	b.DocsURL = docs.URL

	_, out := signed(t, b, key, map[string]any{
		"type": 4, "data": map[string]any{"name": "docs", "options": []map[string]any{{"name": "page", "value": "back", "focused": true}}},
	}, time.Now())
	if s := content(out); out["type"] != float64(8) || !strings.Contains(s, `"Servers › Backups"`) {
		t.Fatalf("autocomplete = %v", out)
	}
	_, out = signed(t, b, key, command("docs", "4", nil, map[string]any{"name": "page", "value": "/servers/backups/"}), time.Now())
	if s := content(out); !strings.Contains(s, docs.URL+"/servers/backups/") || strings.Contains(s, `"flags":64`) {
		t.Fatalf("/docs = %s; want the page, for everyone", s)
	}
}

// TestGateway connects to a fake gateway: the bot identifies online with
// the members intent, answers heartbeats, and gives a linked customer who
// joins the role.
func TestGateway(t *testing.T) {
	f := &fakeDiscord{}
	b, _ := newBot(t, fixture(t), f)
	identified := make(chan map[string]any, 1)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		ctx := r.Context()
		write := func(v any) {
			data, _ := json.Marshal(v)
			_ = c.Write(ctx, websocket.MessageText, data)
		}
		write(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}})
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var id struct {
			Op int            `json:"op"`
			D  map[string]any `json:"d"`
		}
		_ = json.Unmarshal(data, &id)
		identified <- id.D
		write(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{"user": map[string]any{"id": "9", "username": "Raptor Bot"}}})
		write(map[string]any{"op": 0, "s": 2, "t": "GUILD_MEMBER_ADD", "d": map[string]any{"guild_id": guild, "user": map[string]any{"id": "1"}, "roles": []string{}}})
		write(map[string]any{"op": 0, "s": 3, "t": "GUILD_MEMBER_ADD", "d": map[string]any{"guild_id": guild, "user": map[string]any{"id": "4"}, "roles": []string{}}})
		<-ctx.Done()
	}))
	defer gw.Close()
	b.Gateway = "ws" + strings.TrimPrefix(gw.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.session(ctx)
	}()
	select {
	case d := <-identified:
		if d["token"] != "token" || d["intents"] != float64(intentGuildMembers) {
			t.Fatalf("identify = %v", d)
		}
		if p, _ := d["presence"].(map[string]any); p["status"] != "online" {
			t.Fatalf("presence = %v", d["presence"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bot didn't identify")
	}
	want := []string{"PUT /guilds/" + guild + "/members/1/roles/" + customer}
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = append(got, f.took()...)
		if len(got) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	got = append(got, f.took()...)
	if !slices.Equal(got, want) {
		t.Fatalf("calls after members joined = %v, want %v", got, want)
	}
	cancel()
	<-done
}

// TestOnline runs two instances: one holds the gateway (and updates the
// commands), and the other takes over when it stops.
func TestOnline(t *testing.T) {
	takeOver = 50 * time.Millisecond
	t.Cleanup(func() { takeOver = 5 * time.Second })
	db := fixture(t)
	f := &fakeDiscord{}
	var mu sync.Mutex
	open := 0
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		mu.Lock()
		open++
		mu.Unlock()
		defer func() {
			mu.Lock()
			open--
			mu.Unlock()
		}()
		_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`))
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer gw.Close()
	connections := func() int {
		mu.Lock()
		defer mu.Unlock()
		return open
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	var stops []context.CancelFunc
	var done []chan struct{}
	for range 2 {
		b, _ := newBot(t, db, f)
		b.Gateway = "ws" + strings.TrimPrefix(gw.URL, "http")
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan struct{})
		go func() {
			defer close(ch)
			b.Online(ctx)
		}()
		stops, done = append(stops, cancel), append(done, ch)
	}
	waitFor("a gateway connection", func() bool { return connections() == 1 })
	time.Sleep(300 * time.Millisecond)
	if n := connections(); n != 1 {
		t.Fatalf("%d gateway connections with two instances, want 1", n)
	}
	if got := f.took(); !slices.Equal(got, []string{"commands"}) {
		t.Fatalf("calls = %v, want the commands updated once", got)
	}

	// Stop both in turn: whichever had it, the other takes over.
	stops[0]()
	<-done[0]
	waitFor("the other instance to take over", func() bool { return connections() == 1 })
	stops[1]()
	<-done[1]
	waitFor("no connections", func() bool { return connections() == 0 })
}

func TestTickets(t *testing.T) {
	archiveDelay = 0
	t.Cleanup(func() { archiveDelay = 2 * time.Second })
	f := &fakeDiscord{}
	b, key := newBot(t, fixture(t), f)
	b.SupportRole = "300"
	press := func(id, from, channel string) map[string]any {
		_, out := signed(t, b, key, map[string]any{
			"type": 3, "guild_id": guild, "channel_id": channel, "application_id": "1", "token": "tok",
			"member": map[string]any{"user": map[string]any{"id": from, "username": "u" + from}},
			"data":   map[string]any{"custom_id": id},
		}, time.Now())
		return out
	}
	waitFor := func(call string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			f.mu.Lock()
			found := slices.Contains(f.calls, call)
			f.mu.Unlock()
			if found {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s; calls = %v", call, f.took())
			}
		}
	}

	if out := press(ticketButton, "1", "50"); out["type"] != float64(answerModal) {
		t.Fatalf("the ticket button = %v, want the form", out)
	}
	_, out := signed(t, b, key, map[string]any{
		"type": 5, "guild_id": guild, "channel_id": "50", "application_id": "1", "token": "tok",
		"member": map[string]any{"user": map[string]any{"id": "1", "username": "u1"}},
		"data": map[string]any{"custom_id": ticketForm, "components": []map[string]any{
			{"type": 1, "components": []map[string]any{{"type": 4, "custom_id": "topic", "value": "Backups fail"}}},
			{"type": 1, "components": []map[string]any{{"type": 4, "custom_id": "details", "value": "Since Tuesday."}}},
			{"type": 18, "component": map[string]any{"type": 4, "custom_id": "code", "value": "rpt-abcd-2345"}},
		}},
	}, time.Now())
	if out["type"] != float64(answerDeferred) {
		t.Fatalf("the form = %v, want a deferred answer", out)
	}
	waitFor("PATCH /webhooks/1/tok/messages/@original")
	for _, want := range []string{"POST /channels/50/threads", "PUT /channels/777/thread-members/1", "POST /channels/777/messages"} {
		waitFor(want)
	}
	if s := f.body("POST /channels/50/threads"); !strings.Contains(s, `"name":"Backups fail · u1"`) || !strings.Contains(s, `"type":12`) {
		t.Fatalf("thread = %s", s)
	}
	first := f.body("POST /channels/777/messages")
	for _, want := range []string{`\u003c@1\u003e \u003c@\u0026300\u003e`, `"roles":["300"]`, `"users":["1"]`, "Since Tuesday.", "RPT-ABCD-2345", "owner@example.com", "Owner · paid", closeButton} {
		if !strings.Contains(first, want) {
			t.Fatalf("first message = %s, want %q in it", first, want)
		}
	}
	if s := f.body("PATCH /webhooks/1/tok/messages/@original"); !strings.Contains(s, `\u003c#777\u003e`) {
		t.Fatalf("answer = %s", s)
	}
	f.took()

	// One open at a time.
	if s := content(press(ticketButton, "1", "50")); !strings.Contains(s, `open already: \u003c#777\u003e`) {
		t.Fatalf("a second ticket = %s", s)
	}
	// Closing locks and archives the thread; then a new one can be opened.
	if out := press(closeButton, "1", "777"); !strings.Contains(content(out), "Closed by") || strings.Contains(content(out), `"flags":64`) {
		t.Fatalf("close = %v", out)
	}
	waitFor("PATCH /channels/777")
	if s := f.body("PATCH /channels/777"); !strings.Contains(s, `"archived":true`) || !strings.Contains(s, `"locked":true`) {
		t.Fatalf("close = %s", s)
	}
	if out := press(ticketButton, "1", "50"); out["type"] != float64(answerModal) {
		t.Fatalf("after closing, the ticket button = %v, want the form", out)
	}

	// A thread archived by hand doesn't count as open.
	if _, err := b.DB.Exec(context.Background(), `INSERT INTO discord_tickets (thread_id, user_id, topic) VALUES ('777', '2', 'x')`); err == nil {
		t.Fatal("thread IDs are unique")
	}
	if _, err := b.DB.Exec(context.Background(), `UPDATE discord_tickets SET closed_at = NULL`); err != nil {
		t.Fatal(err)
	}
	f.archived = true
	if out := press(ticketButton, "1", "50"); out["type"] != float64(answerModal) {
		t.Fatalf("with the thread archived by hand, the ticket button = %v, want the form", out)
	}
}
