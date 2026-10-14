package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
	"github.com/xena-studios/raptor/internal/panel/subdomains"
)

// fakeRecords is the zone, as subdomains write it.
type fakeRecords struct {
	mu    sync.Mutex
	cname map[string]string
	srv   map[string]string // name -> "target:port"
}

func (f *fakeRecords) SetCNAME(_ context.Context, name, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if target == "" {
		delete(f.cname, name)
	} else {
		f.cname[name] = target
	}
	return nil
}

func (f *fakeRecords) SetSRV(_ context.Context, name string, port int, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if port == 0 {
		delete(f.srv, name)
	} else {
		f.srv[name] = target + ":" + strconv.Itoa(port)
	}
	return nil
}

// Player subdomains: off until the Panel offers them; one namespace across
// orgs, held 30 days after release; records follow the server and node;
// staff can take a name away for good.
func TestSubdomains(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	zone := &fakeRecords{cname: map[string]string{}, srv: map[string]string{}}
	syncer := &subdomains.Syncer{DB: db, DNS: zone, Domain: "raptornodes.net"}
	subs := &orgs.Subdomains{Orgs: orgSvc}
	staffSvc := &staff.Service{DB: db, Auth: authSvc}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Subdomains: subs, Staff: staffSvc, AppOrigin: appOrigin}))
	defer srv.Close()
	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: email, Code: code}}})
		if err != nil {
			t.Fatal(err)
		}
		return b, res.GetUser().GetId()
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	sync := func() {
		t.Helper()
		if err := syncer.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	carol, _ := signUp("carol@example.com")
	newOrg := func(b *browser, name string) string {
		t.Helper()
		res, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetOrg().GetId()
	}
	acme, globex := newOrg(alice, "Acme"), newOrg(carol, "Globex")
	exec("INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", acme, bobID)
	newNode := func(org, short string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', $2, decode(repeat('00', 32), 'hex')) RETURNING id`, org, short).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	acmeNode, globexNode := newNode(acme, "acme1234"), newNode(globex, "glob1234")
	server := func(node, id, cfg string) {
		t.Helper()
		exec(`INSERT INTO m_servers (node_id, server_id, name, version, config, created_at, updated_at) VALUES ($1, $2, 'srv', 1, $3, now(), now())
			ON CONFLICT (node_id, server_id) DO UPDATE SET config = EXCLUDED.config`, node, id, cfg)
	}
	mc := `{"allocations":[{"ip":"0.0.0.0","port":25570,"primary":true}],"egg":{"players":"minecraft"}}`
	server(acmeNode, "s1", mc)
	server(acmeNode, "s2", `{"allocations":[{"ip":"0.0.0.0","port":28015,"primary":true}],"egg":{"players":"source"}}`)
	server(globexNode, "g1", mc)

	// Off: listed as off, and nothing can be made.
	if l, err := alice.subs.ListSubdomains(ctx, &panelv1.ListSubdomainsRequest{OrgId: acme}); err != nil || l.GetDomain() != "" {
		t.Fatalf("off: %v, %v", l, err)
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: "smp"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("creating while off: %v", err)
	}
	subs.Domain = "raptornodes.net"

	for name, want := range map[string]bool{"smp": true, "www": false, "n-abc": false, "Bad_Name": false, "ab": false, "xn--pple": false} {
		res, err := alice.subs.CheckSubdomain(ctx, &panelv1.CheckSubdomainRequest{OrgId: acme, Name: name})
		if err != nil || res.GetAvailable() != want {
			t.Errorf("check %q: %v, %v", name, res, err)
		}
	}
	if _, err := bob.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: "smp"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member creating: %v", err)
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "nope", Name: "smp"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a server that doesn't exist: %v", err)
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: globexNode, ServerId: "g1", Name: "smp"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("another org's node: %v", err)
	}
	made, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: " SMP "})
	if err != nil || made.GetSubdomain().GetName() != "smp" {
		t.Fatalf("create: %v, %v", made, err)
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: "other"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a second name for one server: %v", err)
	}
	if _, err := carol.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: globex, NodeId: globexNode, ServerId: "g1", Name: "smp"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("another org taking the name: %v", err)
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s2", Name: "rust"}); err != nil {
		t.Fatal(err)
	}

	// Records: a CNAME to the node, and SRV for Minecraft only.
	sync()
	node := "n-acme1234.raptornodes.net"
	if zone.cname["smp.raptornodes.net"] != node || zone.srv["_minecraft._tcp.smp.raptornodes.net"] != node+":25570" {
		t.Errorf("smp: %v %v", zone.cname, zone.srv)
	}
	if zone.cname["rust.raptornodes.net"] != node || zone.srv["_minecraft._tcp.rust.raptornodes.net"] != "" {
		t.Errorf("rust: %v %v", zone.cname, zone.srv)
	}
	l, _ := bob.subs.ListSubdomains(ctx, &panelv1.ListSubdomainsRequest{OrgId: acme})
	if len(l.GetSubdomains()) != 2 || !l.GetSubdomains()[1].GetLive() || l.GetDomain() != "raptornodes.net" {
		t.Errorf("a member's list: %v", l)
	}
	if l, _ := carol.subs.ListSubdomains(ctx, &panelv1.ListSubdomainsRequest{OrgId: globex}); len(l.GetSubdomains()) != 0 {
		t.Errorf("another org sees acme's: %v", l)
	}
	// A new port follows.
	server(acmeNode, "s1", `{"allocations":[{"ip":"0.0.0.0","port":25580,"primary":true}],"egg":{"players":"minecraft"}}`)
	sync()
	if zone.srv["_minecraft._tcp.smp.raptornodes.net"] != node+":25580" {
		t.Errorf("after the port changed: %v", zone.srv)
	}
	// A deleted server releases its name.
	exec("DELETE FROM m_servers WHERE node_id = $1 AND server_id = 's2'", acmeNode)
	sync()
	if _, ok := zone.cname["rust.raptornodes.net"]; ok {
		t.Error("a deleted server's records are still there")
	}

	// Deleting: records go; the name is held for the org 30 days.
	if _, err := alice.subs.DeleteSubdomain(ctx, &panelv1.DeleteSubdomainRequest{OrgId: acme, Id: made.GetSubdomain().GetId()}); err != nil {
		t.Fatal(err)
	}
	sync()
	if len(zone.cname)+len(zone.srv) != 0 {
		t.Errorf("records after deleting: %v %v", zone.cname, zone.srv)
	}
	if res, _ := carol.subs.CheckSubdomain(ctx, &panelv1.CheckSubdomainRequest{OrgId: globex, Name: "smp"}); res.GetAvailable() {
		t.Error("another org can take a name released today")
	}
	if _, err := alice.subs.CreateSubdomain(ctx, &panelv1.CreateSubdomainRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: "smp"}); err != nil {
		t.Fatalf("taking it back: %v", err)
	}
	exec("UPDATE subdomains SET released_at = now() - interval '31 days' WHERE name = 'rust'")
	if res, _ := carol.subs.CheckSubdomain(ctx, &panelv1.CheckSubdomainRequest{OrgId: globex, Name: "rust"}); !res.GetAvailable() {
		t.Error("a name released over 30 days ago is still held")
	}

	// Staff take one away for good.
	admin, adminID := signUp("admin@raptor.example")
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')`, adminID, []byte(adminID))
	exec(`UPDATE sessions SET passkey_at = $2 WHERE user_id = $1`, adminID, time.Now())
	ban, err := admin.staff.BanSubdomain(ctx, &panelv1.BanSubdomainRequest{Name: "smp.raptornodes.net", Reason: "phishing page"})
	if err != nil || ban.GetOrgId() != acme {
		t.Fatalf("ban: %v, %v", ban, err)
	}
	sync()
	if len(zone.cname) != 0 {
		t.Errorf("records after a ban: %v", zone.cname)
	}
	if res, _ := alice.subs.CheckSubdomain(ctx, &panelv1.CheckSubdomainRequest{OrgId: acme, Name: "smp"}); res.GetAvailable() {
		t.Error("a banned name can be had again")
	}
}
