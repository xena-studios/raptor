package loadtest_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/loadtest"
	"github.com/xena-studios/raptor/internal/panel/api"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// A small fleet against a real Panel: every node enrolls and links, its
// events reach the mirror, and the Panel fetches what changed.
func TestFleet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := paneltest.NewDB(t)
	_, panelKey, _ := ed25519.GenerateKey(nil)
	reg := &nodes.Registry{DB: pool, PanelKey: panelKey}
	hub := &nodes.Hub{
		PanelKey: panelKey, NodeKey: reg.NodeKey,
		OnConnect: func(ctx context.Context, h nodelink.Hello) { _ = reg.Connected(ctx, h) },
	}
	defer hub.Close()
	mirror := &nodes.Mirror{DB: pool, Hub: hub}
	hub.EventsAvailable = func(_ context.Context, id string, last int64) { mirror.Available(id, last) }
	srv := httptest.NewServer(api.Handler(api.Config{Nodes: reg, Hub: hub}))
	defer srv.Close()

	org, err := reg.CreateOrg(ctx, "Load test")
	if err != nil {
		t.Fatal(err)
	}
	const fleet, servers = 20, 3
	tokens := make([]string, fleet)
	for i := range tokens {
		if tokens[i], err = reg.CreateJoinToken(ctx, org, ""); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := loadtest.Enroll(ctx, srv.URL, http.DefaultClient, tokens, 5)
	if err != nil {
		t.Fatal(err)
	}

	stats := loadtest.NewStats()
	rctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, id := range ids {
		n := loadtest.NewNode(id, loadtest.Load{Servers: servers, EventEvery: 50 * time.Millisecond}, stats)
		wg.Go(func() { n.Run(rctx, srv.URL, http.DefaultClient) })
	}
	deadline := time.Now().Add(15 * time.Second)
	var mirrored int
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM m_servers").Scan(&mirrored)
		if s := stats.Snapshot(); s.Up == fleet && s.Pulled > fleet*5 && mirrored == fleet*servers {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	wg.Wait()
	s := stats.Snapshot()
	t.Logf("\n%s", s)
	if s.Connects < fleet || len(s.DialErrors) > 0 {
		t.Errorf("linked %d of %d; dial errors %v", s.Connects, fleet, s.DialErrors)
	}
	if s.Pulled <= fleet*5 || s.Delivery.N == 0 {
		t.Errorf("events pulled %d of %d made", s.Pulled, s.Events)
	}
	if mirrored != fleet*servers {
		t.Errorf("mirrored servers %d, want %d", mirrored, fleet*servers)
	}
	if s.Calls["GetServers"] == 0 {
		t.Error("the Panel never fetched a server")
	}
}
