package nodes

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/shared/nodelink/nodelinktest"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/link"
	"github.com/xena-studios/raptor/internal/wings/server"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

type oneServer struct{ c *server.Console }

func (s oneServer) List() map[string]server.State { return nil }
func (s oneServer) Get(context.Context, string) (*server.Server, error) {
	return nil, errors.New("no")
}

func (s oneServer) Status(id string) (server.Status, error) {
	if id != "s1" {
		return server.Status{}, errors.New("no such server")
	}
	return server.Status{State: server.Running, Console: s.c}, nil
}

// A console watched through the instance that doesn't hold the node: the
// holder streams it and forwards it by notification, in order, with lines
// too long for a notification cut; closing the watch stops the holder's
// stream.
func TestConsoleAcrossInstances(t *testing.T) {
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
	x := &command.Executor{DB: db, NodeID: nodeID, PanelKey: r.PanelKey.Public().(ed25519.PublicKey)}
	c := server.NewConsole()
	for i := range 1000 {
		c.Backfill(fmt.Sprintf("old %04d %s", i, strings.Repeat("x", 40))) // not counted toward the streaming limit
	}
	proxy := nodelinktest.NewProxy(t, a.addr())
	l := link.New(link.Config{
		PanelURL: "http://" + proxy.Addr, NodeID: nodeID, NodeKey: nodeKey, PanelKey: res.GetPanelKey(),
		Commands: x, Servers: func() link.Servers { return oneServer{c} },
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := a.hub.Wait(wctx, nodeID); err != nil {
		t.Fatal(err)
	}

	id, _ := uuid.NewV7()
	e := command.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: "u", Action: link.ConsoleAction, ServerID: "s1", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: "s1", ExpiresAt: e.ExpiresAt}
	p, _ := e.Grant.Payload()
	e.Grant.Signature = ed25519.Sign(r.PanelKey, p)
	env, _ := json.Marshal(e)

	wctx2, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	got := make(chan ConsoleBatch, 1000)
	done := make(chan error, 1)
	go func() {
		done <- b.router.Console(wctx2, nodeID, env, func(cb ConsoleBatch) error {
			got <- cb
			return nil
		})
	}()
	var history []string
	for len(history) < 1000 {
		select {
		case cb := <-got:
			if !cb.History {
				t.Fatalf("live lines inside the history")
			}
			history = append(history, cb.Lines...)
		case <-wctx2.Done():
			t.Fatalf("got %d history lines", len(history))
		}
	}
	for i, l := range history {
		if !strings.HasPrefix(l, fmt.Sprintf("old %04d ", i)) {
			t.Fatalf("history line %d is %q", i, l)
		}
	}
	long := strings.Repeat("\x1b[1m!", 5000)
	c.Write("new")
	c.Write(long)
	var live []string
	for len(live) < 2 {
		select {
		case cb := <-got:
			live = append(live, cb.Lines...)
		case <-wctx2.Done():
			t.Fatalf("live lines: %q", live)
		}
	}
	if live[0] != "new" || !strings.HasSuffix(live[1], "…") || len(live[1]) >= len(long) || jsonLen(live[1]) > lineMaxFwd {
		t.Errorf("live: %q, then a line of %d bytes", live[0], len(live[1]))
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("watch ended with %v", err)
	}
	// The holder stops its stream.
	for range 200 {
		a.router.mu.Lock()
		n := len(a.router.serving)
		a.router.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Error("the holding instance kept streaming after the watcher left")
}

func TestSplitBatch(t *testing.T) {
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = strings.Repeat("a", 100)
	}
	parts := splitBatch(ConsoleBatch{Lines: lines, History: true})
	n := 0
	for _, p := range parts {
		b, _ := json.Marshal(consoleMsg{Sub: uuid.NewString(), ConsoleBatch: p})
		if len(b) > 8000-4 {
			t.Errorf("a part is %d bytes as a notification", len(b))
		}
		if !p.History {
			t.Error("a part lost History")
		}
		n += len(p.Lines)
	}
	if n != 300 || len(parts) < 4 {
		t.Errorf("%d lines in %d parts", n, len(parts))
	}
	if p := splitBatch(ConsoleBatch{History: true}); len(p) != 1 || !p[0].History {
		t.Errorf("an empty history batch: %+v", p)
	}
}
