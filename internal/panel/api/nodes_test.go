package api

import (
	"context"
	"crypto/ed25519"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
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

// A node asks which address it reaches the Panel from (raptor doctor's
// hostname check): the trusted header's, behind Cloudflare.
func TestNodeAddress(t *testing.T) {
	hub := &nodes.Hub{ClientIPHeader: "CF-Connecting-IP"}
	defer hub.Close()
	srv := httptest.NewServer(Handler(Config{Hub: hub}))
	defer srv.Close()
	for header, want := range map[string]string{"203.0.113.7": "203.0.113.7\n", "": "127.0.0.1\n", "2001:db8::1": "2001:db8::1\n"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+nodelink.AddressPath, nil)
		if header != "" {
			req.Header.Set("CF-Connecting-IP", header)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if string(b) != want {
			t.Errorf("header %q: %q, want %q", header, b, want)
		}
	}
}

// A dual-stack node shows the Panel its address in both families, however
// its control connection went: Wings opens an address connection over
// IPv4 and one over IPv6, and the Panel records each where it came from
// (docs/ARCHITECTURE.md#node-dns). The Panel listens on 127.0.0.1 and ::1,
// at localhost.
func TestNodeAddressesBothFamilies(t *testing.T) {
	if ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip6", "localhost"); err != nil || len(ips) == 0 {
		t.Skip("localhost has no IPv6 address here")
	}
	l4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l4.Addr().(*net.TCPAddr).Port
	l6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		_ = l4.Close()
		t.Skipf("no IPv6 loopback on port %d: %v", port, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := paneltest.NewDB(t)
	_, panelKey, _ := ed25519.GenerateKey(nil)
	reg := &nodes.Registry{DB: pool, PanelKey: panelKey}
	addrs := &nodes.Addresses{Store: reg}
	seen := make(chan string, 16)
	hub := &nodes.Hub{
		PanelKey: panelKey, NodeKey: reg.NodeKey,
		OnAddress: func(ctx context.Context, h nodelink.Hello, a netip.Addr) {
			addrs.Seen(ctx, h, a)
			seen <- h.Purpose + " " + a.String()
		},
	}
	defer hub.Close()
	h := Handler(Config{Nodes: reg, Hub: hub})
	for _, l := range []net.Listener{l4, l6} {
		s := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = s.Serve(l) }()
		defer func() { _ = s.Close() }()
	}
	panelURL := "http://localhost:" + strconv.Itoa(port)

	org, _ := reg.CreateOrg(ctx, "org")
	token, _ := reg.CreateJoinToken(ctx, org, "")
	_, nodeKey, _ := ed25519.GenerateKey(nil)
	pub := nodeKey.Public().(ed25519.PublicKey)
	payload, _ := nodelink.EnrollPayload(token, pub)
	res, err := nodev1connect.NewEnrollmentServiceClient(http.DefaultClient, panelURL+"/api").Enroll(ctx, &nodev1.EnrollRequest{
		Token: token, PublicKey: pub, Signature: ed25519.Sign(nodeKey, payload), Name: "box", WingsVersion: "9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	l := link.New(link.Config{PanelURL: panelURL, NodeID: res.GetNodeId(), NodeKey: nodeKey, PanelKey: res.GetPanelKey(), Software: "9.9.9"})
	go func() { _ = l.Run(ctx) }()

	// The control connection, then an address connection in each family.
	got := map[string]bool{}
	timeout := time.After(10 * time.Second)
	for !got["address 127.0.0.1"] || !got["address ::1"] {
		select {
		case p := <-seen:
			got[p] = true
		case <-timeout:
			t.Fatalf("addresses seen: %v", got)
		}
	}
	// The address connections never become the node's connection.
	c, err := hub.Wait(ctx, res.GetNodeId())
	if err != nil || c.Hello.Purpose != nodelink.PurposeControl {
		t.Fatalf("node's connection: %+v, %v", c, err)
	}
	var v4, v6 string
	if err := pool.QueryRow(ctx, "SELECT coalesce(host(public_ipv4), ''), coalesce(host(public_ipv6), '') FROM nodes WHERE id = $1", res.GetNodeId()).Scan(&v4, &v6); err != nil {
		t.Fatal(err)
	}
	if v4 != "127.0.0.1" || v6 != "::1" {
		t.Errorf("recorded IPv4 %q, IPv6 %q", v4, v6)
	}
}
