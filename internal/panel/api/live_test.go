package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/live"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// fakeConsoles is a node's console: it checks the grant like Wings, sends
// the history, then whatever is pushed, until the watcher leaves.
type fakeConsoles struct {
	t        *testing.T
	panelKey ed25519.PublicKey
	mu       sync.Mutex
	opened   []nodecmd.Envelope
	push     chan string
}

func (f *fakeConsoles) Console(ctx context.Context, _ string, envelope []byte, out func(nodes.ConsoleBatch) error) error {
	var e nodecmd.Envelope
	if err := json.Unmarshal(envelope, &e); err != nil {
		return err
	}
	p, _ := e.Grant.Payload()
	if !ed25519.Verify(f.panelKey, p, e.Grant.Signature) || e.Grant.Action != "server.console" || e.Grant.ServerID != e.ServerID {
		f.t.Errorf("bad console grant: %+v", e)
	}
	f.mu.Lock()
	f.opened = append(f.opened, e)
	f.mu.Unlock()
	if err := out(nodes.ConsoleBatch{Lines: []string{"history"}, History: true}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case l := <-f.push:
			if err := out(nodes.ConsoleBatch{Lines: []string{l}}); err != nil {
				return err
			}
		}
	}
}

func (f *fakeConsoles) opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.opened)
}

// The live socket: signed-in users of the web app's origin only; a console
// is watched with the user's access checked and a grant for it, reopened
// (history again, with Reset) to check access again; a member without
// console.read gets an error, not lines.
func TestLiveConsole(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	consoles := &fakeConsoles{t: t, panelKey: pub, push: make(chan string, 10)}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	cmds := &commands.Service{Auth: authSvc, Consoles: consoles, PanelKey: key}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, Commands: cmds, AppOrigin: appOrigin,
		ConsoleRecheck: 500 * time.Millisecond,
	}))
	defer srv.Close()

	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return b, res.GetUser().GetId()
	}
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'live1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'mc', 1, now(), now())`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1", UserId: bobID, Permissions: []string{"power"}}); err != nil {
		t.Fatal(err)
	}

	wsURL := "wss" + strings.TrimPrefix(srv.URL, "https") + "/api/live"
	dial := func(b *browser, origin string) (*websocket.Conn, int, error) {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: b.http, HTTPHeader: h})
		status := 0
		if resp != nil {
			status = resp.StatusCode
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		return c, status, err
	}
	recv := func(c *websocket.Conn) live.Out {
		t.Helper()
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var m live.Out
		if err := wsjson.Read(rctx, c, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// Signed out, and another origin: refused.
	if _, status, err := dial(newBrowser(t, srv, appOrigin), appOrigin); err == nil || status != http.StatusUnauthorized {
		t.Errorf("signed out: %v", err)
	}
	if _, _, err := dial(alice, "https://evil.example"); err == nil {
		t.Error("another origin connected")
	}

	c, _, err := dial(alice, appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := wsjson.Write(ctx, c, live.In{Op: "console", ID: "a", Node: nodeID, Server: "s1"}); err != nil {
		t.Fatal(err)
	}
	if m := recv(c); m.ID != "a" || m.Type != "lines" || !m.Reset || m.Lines[0] != "history" {
		t.Fatalf("first message: %+v", m)
	}
	consoles.push <- "live line"
	if m := recv(c); m.Lines[0] != "live line" || m.Reset {
		t.Fatalf("live: %+v", m)
	}
	// Reopened to check access again: the history again, with Reset.
	if m := recv(c); !m.Reset || m.Lines[0] != "history" || consoles.opens() < 2 {
		t.Fatalf("after the recheck: %+v (%d opens)", m, consoles.opens())
	}

	// Bob may start the server but not watch its console.
	cb, _, err := dial(bob, appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cb.CloseNow() }()
	if err := wsjson.Write(ctx, cb, live.In{Op: "console", ID: "b", Node: nodeID, Server: "s1"}); err != nil {
		t.Fatal(err)
	}
	if m := recv(cb); m.Type != "ended" || m.Error == "" || m.Retry {
		t.Fatalf("member without console.read: %+v", m)
	}
	if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1", UserId: bobID, Permissions: []string{"console.read"}}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, cb, live.In{Op: "console", ID: "b2", Node: nodeID, Server: "s1"}); err != nil {
		t.Fatal(err)
	}
	if m := recv(cb); m.Type != "lines" || m.Lines[0] != "history" {
		t.Fatalf("member with console.read: %+v", m)
	}
}
