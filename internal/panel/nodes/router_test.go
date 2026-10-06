package nodes

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/shared/nodelink/nodelinktest"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/link"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

type instance struct {
	hub    *Hub
	router *Router
	srv    *httptest.Server
}

func newInstance(t *testing.T, ctx context.Context, r *Registry) *instance {
	t.Helper()
	in := &instance{}
	in.router = &Router{DB: r.DB, ID: NewInstanceID()}
	in.hub = &Hub{
		PanelKey: r.PanelKey, NodeKey: r.NodeKey,
		OnConnect:    func(ctx context.Context, h nodelink.Hello) { in.router.Connected(ctx, h) },
		OnDisconnect: func(ctx context.Context, id string) { in.router.Disconnected(ctx, id) },
	}
	in.router.Hub = in.hub
	if err := in.router.Start(ctx); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, in.hub)
	in.srv = httptest.NewServer(mux)
	t.Cleanup(func() { in.hub.Close(); in.srv.Close() })
	return in
}

func (in *instance) addr() string { return strings.TrimPrefix(in.srv.URL, "http://") }

// Two Panel instances, one node: a command sent through either reaches it,
// and draining the instance holding it moves it to the other without a
// command running twice.
func TestRouter(t *testing.T) {
	r := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := newInstance(t, ctx, r), newInstance(t, ctx, r)

	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, nodeKey, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, nodeKey))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := res.GetNodeId()

	db, err := wstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var runs atomic.Int32
	x := &command.Executor{DB: db, NodeID: nodeID, PanelKey: r.PanelKey.Public().(ed25519.PublicKey)}
	x.Register("server.start", command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		runs.Add(1)
		return map[string]bool{"ok": true}, nil
	}})
	proxy := nodelinktest.NewProxy(t, a.addr())
	l := link.New(link.Config{
		PanelURL: "http://" + proxy.Addr, NodeID: nodeID, NodeKey: nodeKey, PanelKey: res.GetPanelKey(),
		Commands: x, MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := a.hub.Wait(wctx, nodeID); err != nil {
		t.Fatal(err)
	}

	envelope := func() []byte {
		id, _ := uuid.NewV7()
		e := command.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: "u", Action: "server.start", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
		e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ExpiresAt: e.ExpiresAt}
		p, _ := e.Grant.Payload()
		e.Grant.Signature = ed25519.Sign(r.PanelKey, p)
		out, _ := json.Marshal(e)
		return out
	}
	execute := func(via *instance, env []byte) {
		t.Helper()
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		res, err := via.router.Execute(cctx, nodeID, env)
		if err != nil || res.GetError() != "" || string(res.GetResult()) != `{"ok":true}` {
			t.Fatalf("execute via %s: %v, %v", via.router.ID[:6], res, err)
		}
	}

	// Through the instance holding the node, and forwarded from the other.
	execute(a, envelope())
	env := envelope()
	execute(b, env)
	execute(b, env) // a retry: the stored result, not a second run
	if n := runs.Load(); n != 2 {
		t.Fatalf("ran %d times, want 2", n)
	}

	// Drain a: the node moves to b, and commands through a are forwarded.
	proxy.SetTarget(b.addr())
	a.router.Stop(ctx)
	a.hub.Drain(ctx, 100*time.Millisecond)
	if _, err := b.hub.Wait(wctx, nodeID); err != nil {
		t.Fatalf("the node didn't move to the other instance: %v", err)
	}
	execute(b, envelope())
	execute(a, envelope())
	if n := runs.Load(); n != 4 {
		t.Fatalf("ran %d times, want 4", n)
	}
	// A draining instance turns nodes away.
	resp, err := http.Get(a.srv.URL + nodelink.Path)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("draining instance answered %d", resp.StatusCode)
	}
}
