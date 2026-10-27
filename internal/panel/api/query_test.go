package api

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/link"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

// nodeQueries is a node's side of streamed queries: "rows" answers a
// result, "update" changes 3 rows, and "sleep" runs until it's stopped.
type nodeQueries struct {
	stopped chan string
}

func (n *nodeQueries) HasQuery(id string) bool {
	return id == "rows" || id == "update" || id == "sleep"
}

func (n *nodeQueries) StreamQuery(ctx context.Context, id string, w io.Writer, flush func()) error {
	line := func(s string) {
		_, _ = io.WriteString(w, s+"\n")
		flush()
	}
	switch id {
	case "rows":
		line(`{"columns":[{"name":"id"}]}`)
		line(`{"rows":[["1"],["2"]]}`)
		line(`{"done":{"statements":1,"affected":-1,"rows":2,"shown":2,"millis":1}}`)
	case "update":
		line(`{"done":{"statements":1,"affected":3,"rows":0,"shown":0,"millis":1}}`)
	case "sleep":
		// Nothing yet: a query that takes minutes to return its first row.
		<-ctx.Done()
		n.stopped <- id
		return ctx.Err()
	}
	return nil
}

// Streamed queries through the Panel to a real node link: the command
// starts the query (with the user's access checked), its results come
// over a transfer connection as lines, the activity keeps how many rows
// it changed, closing the response stops it on the node, and a node from
// before streaming has its whole result sent as lines.
func TestQueryStream(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mail := &inbox{}
	panelPub, panelKey, _ := ed25519.GenerateKey(rand.Reader)
	nodePub, nodeKey, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}

	signUp := func(srv *httptest.Server, email string) (*browser, string) {
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

	var nodeID string
	hub := &nodes.Hub{
		PanelKey: panelKey,
		NodeKey: func(_ context.Context, id string) (ed25519.PublicKey, error) {
			if id != nodeID {
				return nil, nodelink.ErrUnknownNode
			}
			return nodePub, nil
		},
	}
	defer hub.Close()
	cmds := &commands.Service{Auth: authSvc, Sender: hub, PanelKey: panelKey}
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, hub)
	mux.Handle("/", Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, Commands: cmds, Transfers: hub, AppOrigin: appOrigin}))
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	alice, _ := signUp(srv, "alice@example.com")
	bob, bobID := signUp(srv, "bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', $2) RETURNING id`, org, []byte(nodePub)).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'mc', 1, now(), now())`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1", UserId: bobID, Permissions: []string{"databases.read"}}); err != nil {
		t.Fatal(err)
	}

	// The node: a real link and executor, with the queries faked.
	nq := &nodeQueries{stopped: make(chan string, 1)}
	wdb, err := wstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wdb.Close() }()
	x := &command.Executor{DB: wdb, NodeID: nodeID, PanelKey: panelPub}
	start := func(e command.Envelope) (any, error) {
		var p struct {
			SQL    string `json:"sql"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(e.Params, &p)
		if p.SQL == "old" {
			// A Wings from before streaming: the whole result.
			return map[string]any{"columns": []map[string]string{{"name": "n"}}, "rows": [][]string{{"7"}}, "affected": -1, "millis": 2}, nil
		}
		if !p.Stream {
			return nil, fmt.Errorf("not streamed")
		}
		return map[string]string{"query_id": p.SQL}, nil
	}
	x.Register("db.query", command.Handler{Signed: command.Never, Run: func(_ context.Context, e command.Envelope) (any, error) { return start(e) }})
	x.Register("db.query.read", command.Handler{Signed: command.Never, ReadOnly: true, Run: func(_ context.Context, e command.Envelope) (any, error) { return start(e) }})
	l := link.New(link.Config{
		PanelURL: srv.URL, NodeID: nodeID, NodeKey: nodeKey, PanelKey: panelPub, Commands: x,
		Queries: func() link.Queries { return nq }, HTTPClient: srv.Client(),
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := hub.Wait(wctx, nodeID); err != nil {
		t.Fatal(err)
	}

	post := func(ctx context.Context, b *browser, action, sql string) *http.Response {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"node": nodeID, "server": "s1", "action": action, "params": map[string]any{"id": "d1", "sql": sql}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/databases/query", strings.NewReader(string(body)))
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := b.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// run posts a query and reads its whole answer.
	run := func(b *browser, action, sql string) (int, string, []string) {
		t.Helper()
		resp := post(ctx, b, action, sql)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), strings.Split(strings.TrimSpace(string(body)), "\n")
	}

	code, ctype, got := run(alice, "db.query", "rows")
	if code != http.StatusOK || ctype != "application/x-ndjson" {
		t.Fatalf("a query: %d %s", code, ctype)
	}
	if len(got) != 4 || got[0] != `{"query_id":"rows"}` || got[2] != `{"rows":[["1"],["2"]]}` || !strings.HasPrefix(got[3], `{"done":`) {
		t.Errorf("a query's lines: %q", got)
	}

	// The activity keeps how many rows a change changed, never its SQL.
	if _, _, got := run(alice, "db.query", "update"); len(got) != 2 {
		t.Errorf("an update's lines: %q", got)
	}
	var meta map[string]any
	if err := db.QueryRow(ctx, `SELECT metadata FROM audit_log WHERE action = 'command' AND metadata->>'action' = 'db.query' ORDER BY at DESC, id DESC LIMIT 1`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["rows_changed"] != float64(3) {
		t.Errorf("the update's activity: %v", meta)
	}

	// Bob may look, not change.
	if code, _, _ := run(bob, "db.query", "rows"); code != http.StatusForbidden {
		t.Errorf("db.query without databases.write: %d", code)
	}
	if _, _, got := run(bob, "db.query.read", "rows"); len(got) != 4 {
		t.Errorf("db.query.read: %q", got)
	}
	if code, _, _ := run(alice, "files.list", "rows"); code != http.StatusBadRequest {
		t.Errorf("another action: %d", code)
	}

	// A Wings from before streaming: its whole result, as lines.
	if _, _, got := run(alice, "db.query", "old"); len(got) != 3 || got[1] != `{"rows":[["7"]]}` {
		t.Errorf("an old node's result: %q", got)
	}

	// A query that hasn't returned anything yet still answers at once
	// (proxies drop a response with no first byte), and closing the
	// response stops the query on the node.
	qctx, qcancel := context.WithCancel(ctx)
	defer qcancel()
	first := make(chan string, 1)
	go func() {
		body, _ := json.Marshal(map[string]any{"node": nodeID, "server": "s1", "action": "db.query", "params": map[string]any{"id": "d1", "sql": "sleep"}})
		req, _ := http.NewRequestWithContext(qctx, http.MethodPost, srv.URL+"/api/databases/query", strings.NewReader(string(body)))
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := alice.http.Do(req)
		if err != nil {
			first <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		l, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- l
		<-qctx.Done()
	}()
	select {
	case l := <-first:
		if l != "{\"query_id\":\"sleep\"}\n" {
			t.Errorf("a slow query's first line: %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Error("a slow query's first line didn't come")
	}
	qcancel()
	select {
	case <-nq.stopped:
	case <-time.After(10 * time.Second):
		t.Error("the query wasn't stopped when its response was closed")
	}
}
