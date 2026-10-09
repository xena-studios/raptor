package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The connection test dials the node's own address on the server's ports,
// and tells which answered and why the others didn't.
func TestConnectionTest(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	var dialed []string
	orgSvc := &orgs.Service{
		DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			switch addr {
			case "203.0.113.7:25565":
				a, _ := net.Pipe() // stays open, like a game waiting for the player
				return a, nil
			case "203.0.113.7:25569":
				a, b := net.Pipe() // hangs up at once, like a forwarder with nothing behind it
				_ = b.Close()
				return a, nil
			case "203.0.113.7:25566":
				return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
			}
			return nil, context.DeadlineExceeded
		},
		LookupAddr: func(context.Context, string) ([]string, error) {
			return []string{"static.7.113.0.203.clients.your-server.de."}, nil
		},
	}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code}}}); err != nil {
		t.Fatal(err)
	}
	created, _ := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	org := created.GetOrg().GetId()
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, public_ipv4) VALUES ($1, 'box', 'conn1234', decode(repeat('00', 32), 'hex'), '203.0.113.7') RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, config) VALUES ($1, 's1', 'mc', 1, now(), now(),
		'{"allocations": [{"ip": "0.0.0.0", "port": 25566}, {"ip": "0.0.0.0", "port": 25565, "primary": true}, {"ip": "0.0.0.0", "port": 25567}, {"ip": "127.0.0.1", "port": 25568}, {"ip": "0.0.0.0", "port": 25569}]}')`, node); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := b.orgs.TestConnection(ctx, &panelv1.TestConnectionRequest{OrgId: org, NodeId: node, ServerId: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Error("the probes didn't run at the same time")
	}
	got := map[int32]*panelv1.PortProbe{}
	for _, p := range res.GetPorts() {
		got[p.GetPort()] = p
	}
	if res.GetAddress() != "203.0.113.7" || res.GetProvider() != "hetzner" || res.GetPorts()[0].GetPort() != 25565 {
		t.Errorf("result: %v", res)
	}
	if !got[25565].GetReachable() || got[25566].GetFailure() != "refused" || got[25567].GetFailure() != "timeout" || got[25568].GetFailure() != "local_only" || got[25569].GetFailure() != "closed" {
		t.Errorf("ports: %v", res.GetPorts())
	}
	for _, a := range dialed {
		if a == "203.0.113.7:25568" {
			t.Error("a 127.0.0.1 allocation was dialed")
		}
	}
	// Someone without access to the server can't make the Panel dial it.
	other := newBrowser(t, srv, appOrigin)
	if _, err := other.orgs.TestConnection(ctx, &panelv1.TestConnectionRequest{OrgId: org, NodeId: node, ServerId: "s1"}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("signed out: %v", err)
	}
}

func TestGuessProvider(t *testing.T) {
	for rdns, want := range map[string]string{
		"ec2-3-1-2-3.compute-1.amazonaws.com":     "aws",
		"vps-1a2b3c4d.vps.ovh.net":                "ovh",
		"static.88.99.1.2.clients.your-server.de": "hetzner",
		"c-73-1-2-3.hsd1.ca.comcast.net":          "home",
		"pool-71-1-2-3.nycmny.fios.verizon.net":   "home",
		"mail.example.com":                        "",
		"":                                        "",
	} {
		if got := orgs.GuessProvider(rdns); got != want {
			t.Errorf("%q: %q, want %q", rdns, got, want)
		}
	}
}
