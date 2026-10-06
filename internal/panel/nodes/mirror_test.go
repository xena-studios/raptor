package nodes

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/link"
	"github.com/xena-studios/raptor/internal/wings/server"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

// fakeServers is the node's server manager.
type fakeServers struct {
	mu      sync.Mutex
	servers map[string]*server.Server
	states  map[string]server.State
}

func (f *fakeServers) List() map[string]server.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]server.State{}
	for id := range f.servers {
		out[id] = f.states[id]
	}
	return out
}

func (f *fakeServers) Get(_ context.Context, id string) (*server.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.servers[id]
	if !ok {
		return nil, server.ErrNotFound
	}
	c := *s
	return &c, nil
}

func (f *fakeServers) Status(id string) (server.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return server.Status{State: f.states[id]}, nil
}

func (f *fakeServers) set(id, name string, version int64, st server.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &server.Server{ID: id, Version: version, DesiredState: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.Name = name
	f.servers[id], f.states[id] = s, st
}

func (f *fakeServers) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.servers, id)
}

// A real node (link, outbox) and a real Panel (hub, registry, mirror on
// Postgres): the mirror follows the node's servers, and rebuilds after it's
// dropped or falls out of step.
func TestMirror(t *testing.T) {
	r := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := &Hub{PanelKey: r.PanelKey, NodeKey: r.NodeKey}
	defer hub.Close()
	mirror := &Mirror{DB: r.DB, Hub: hub}
	hub.EventsAvailable = func(_ context.Context, id string, _ int64) { mirror.Notify(id) }
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, hub)
	srv := httptest.NewServer(mux)
	defer srv.Close()

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
	outbox := events.New(db)
	fs := &fakeServers{servers: map[string]*server.Server{}, states: map[string]server.State{}}
	fs.set("s1", "Survival", 1, server.Running)
	l := link.New(link.Config{
		PanelURL: srv.URL, NodeID: nodeID, NodeKey: nodeKey, PanelKey: res.GetPanelKey(),
		Events: outbox, Servers: func() link.Servers { return fs },
	})
	go func() { _ = l.Run(ctx) }()

	type row struct{ name, state string }
	rows := func() map[string]row {
		id, _ := uuid.Parse(nodeID)
		list, err := store.New(r.DB).ListMirrorServers(ctx, pgUUID(id))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]row{}
		for _, s := range list {
			out[s.ServerID] = row{s.Name, s.State}
		}
		return out
	}
	waitFor := func(what string, ok func(map[string]row) bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got := rows()
			if ok(got) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: mirror is %v", what, got)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	appendEvent := func(typ, sid string, data map[string]any) {
		if _, err := outbox.Append(ctx, events.Event{Type: typ, ServerID: sid, Data: data}); err != nil {
			t.Fatal(err)
		}
	}

	// The first sync is a snapshot.
	waitFor("snapshot", func(m map[string]row) bool { return m["s1"] == row{"Survival", "running"} })

	// A new server, a rename, a state change, a deletion.
	fs.set("s2", "Creative", 1, server.Installing)
	appendEvent("server.created", "s2", nil)
	waitFor("created", func(m map[string]row) bool { return m["s2"] == row{"Creative", "installing"} })
	fs.set("s1", "Survival 2", 2, server.Running)
	appendEvent("server.updated", "s1", nil)
	waitFor("updated", func(m map[string]row) bool { return m["s1"].name == "Survival 2" })
	appendEvent("server.state", "s2", map[string]any{"state": "running"})
	waitFor("state", func(m map[string]row) bool { return m["s2"].state == "running" })
	fs.remove("s1")
	appendEvent("server.deleted", "s1", nil)
	waitFor("deleted", func(m map[string]row) bool { _, ok := m["s1"]; return !ok && len(m) == 1 })

	// Dropped: rebuilt from a snapshot on the next sync.
	if err := mirror.Reset(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	fs.set("s3", "Modded", 1, server.Offline)
	mirror.Notify(nodeID)
	waitFor("rebuilt", func(m map[string]row) bool {
		ids := make([]string, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return slices.Equal(ids, []string{"s2", "s3"})
	})

	// The Panel ahead of the node (its state.db restored from a snapshot):
	// the node says so, and the mirror takes a snapshot.
	id, _ := uuid.Parse(nodeID)
	if err := store.New(r.DB).SetNodeAcked(ctx, store.SetNodeAckedParams{ID: pgUUID(id), LastAckedSeq: 9999}); err != nil {
		t.Fatal(err)
	}
	fs.set("s4", "Late", 1, server.Offline)
	mirror.Notify(nodeID)
	waitFor("resynced", func(m map[string]row) bool { _, ok := m["s4"]; return ok })
	if acked, _ := store.New(r.DB).GetNodeAcked(ctx, pgUUID(id)); acked == 9999 {
		t.Error("acked wasn't reset by the snapshot")
	}
}
