package api

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/link"
)

// A node enrolls through the API with a join token and then connects with
// the identity it got, as `raptor link` and Wings do.
func TestEnrollAndConnect(t *testing.T) {
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
	srv := httptest.NewServer(Handler(Config{Nodes: reg, Hub: hub}))
	defer srv.Close()

	org, err := reg.CreateOrg(ctx, "org")
	if err != nil {
		t.Fatal(err)
	}
	token, err := reg.CreateJoinToken(ctx, org, "")
	if err != nil {
		t.Fatal(err)
	}
	_, nodeKey, _ := ed25519.GenerateKey(nil)
	pub := nodeKey.Public().(ed25519.PublicKey)
	payload, _ := nodelink.EnrollPayload(token, pub)
	res, err := nodev1connect.NewEnrollmentServiceClient(http.DefaultClient, srv.URL+"/api").Enroll(ctx, &nodev1.EnrollRequest{
		Token: token, PublicKey: pub, Signature: ed25519.Sign(nodeKey, payload), Name: "box", WingsVersion: "9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}

	l := link.New(link.Config{
		PanelURL: srv.URL, NodeID: res.GetNodeId(), NodeKey: nodeKey, PanelKey: res.GetPanelKey(), Software: "9.9.9",
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := hub.Wait(wctx, res.GetNodeId()); err != nil {
		t.Fatalf("the enrolled node didn't connect: %v (%+v)", err, l.Status())
	}
	var seen pgtype.Timestamptz
	var version string
	deadline := time.Now().Add(5 * time.Second)
	for !seen.Valid && time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, "SELECT last_seen_at, wings_version FROM nodes WHERE id = $1", res.GetNodeId()).Scan(&seen, &version); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seen.Valid || version != "9.9.9" {
		t.Errorf("node record: seen %v, version %q", seen, version)
	}
}
