package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// fakeNode checks every command's grant the way Wings does and answers.
type fakeNode struct {
	t        *testing.T
	panelKey ed25519.PublicKey
	mu       sync.Mutex
	got      []nodecmd.Envelope
	fail     string // the next command "runs and fails" with this
}

func (n *fakeNode) Execute(_ context.Context, node string, raw []byte) (*nodev1.ExecuteResponse, error) {
	var e nodecmd.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	p, err := e.Grant.Payload()
	if err != nil || !ed25519.Verify(n.panelKey, p, e.Grant.Signature) {
		n.t.Errorf("bad grant signature on %s", e.Action)
		return nil, errors.New("bad grant")
	}
	if e.Grant.UserID != e.UserID || e.Grant.CommandID != e.CommandID || e.Grant.Action != e.Action ||
		e.Grant.ServerID != e.ServerID || e.Grant.NodeID != node || e.Grant.ExpiresAt != e.ExpiresAt {
		n.t.Errorf("grant doesn't match the command: %+v", e)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, e)
	if n.fail != "" {
		msg := n.fail
		n.fail = ""
		return &nodev1.ExecuteResponse{Error: msg}, nil
	}
	return &nodev1.ExecuteResponse{Result: []byte(`{"ok":true}`)}, nil
}

func (n *fakeNode) last() nodecmd.Envelope {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.got[len(n.got)-1]
}

func TestCommands(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	node := &fakeNode{t: t, panelKey: pub}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc},
		Commands: &commands.Service{Auth: authSvc, Sender: node, PanelKey: key}, AppOrigin: appOrigin,
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
	alice, aliceID := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	dave, _ := signUp("dave@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, facts) VALUES ($1, 'box', 'abcd1234', decode(repeat('00', 32), 'hex'),
		'{"os": "debian", "arch": "arm64", "cpus": 4, "memory_bytes": 8589934592}') RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"s1", "s2"} {
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, install_state, config) VALUES ($1, $2, 'mc', 1, now(), now(), 'installed',
			'{"allocations": [{"ip": "0.0.0.0", "port": 25566}, {"ip": "0.0.0.0", "port": 25565, "primary": true}]}')`, nodeID, s); err != nil {
			t.Fatal(err)
		}
	}
	run := func(b *browser, action, server string) (*panelv1.ExecuteResponse, error) {
		return b.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: action, ServerId: server, ParamsJson: `{}`})
	}

	// The owner can do anything; the grant names her and this command.
	res, err := run(alice, "server.start", "s1")
	if err != nil || res.GetResultJson() != `{"ok":true}` {
		t.Fatalf("owner start: %v, %v", res, err)
	}
	e := node.last()
	if e.UserID != aliceID || e.ServerID != "s1" || e.CommandID != res.GetCommandId() {
		t.Errorf("envelope: %+v", e)
	}
	if left := time.Until(time.Unix(e.ExpiresAt, 0)); left <= 0 || left > commands.DefaultLifetime {
		t.Errorf("expires in %s", left)
	}

	// A member with no grant can't tell the server exists; an outsider can't
	// tell the node does.
	if _, err := run(bob, "server.start", "s1"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("member without a grant: %v", err)
	}
	if _, err := run(dave, "server.start", "s1"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("outsider: %v", err)
	}

	if nodes, err := bob.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org}); err != nil || len(nodes.GetNodes()) != 0 {
		t.Errorf("member without access sees nodes: %v, %v", nodes, err)
	}

	// Granting: admins and owners only, real permissions, members only.
	set := func(b *browser, server, user string, perms ...string) error {
		_, err := b.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: server, UserId: user, Permissions: perms})
		return err
	}
	if err := set(bob, "s1", bobID, "power"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member granting themselves: %v", err)
	}
	if err := set(alice, "s1", bobID, "root"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("unknown permission: %v", err)
	}
	if err := set(alice, "nope", bobID, "power"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown server: %v", err)
	}
	if err := set(alice, "s1", uuid.NewString(), "power"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("granting to a non-member: %v", err)
	}
	if err := set(alice, "s1", bobID, "power", "files.read", "power"); err != nil {
		t.Fatal(err)
	}
	access, err := alice.orgs.ListServerAccess(ctx, &panelv1.ListServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1"})
	if err != nil || len(access.GetAccess()) != 1 || strings.Join(access.GetAccess()[0].GetPermissions(), ",") != "files.read,power" {
		t.Fatalf("access: %v, %v", access, err)
	}

	// What each of them sees: admins and owners everything, members only
	// the servers they have access to (and the nodes those are on).
	if _, err := db.Exec(ctx, "INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')", nodeID); err != nil {
		t.Fatal(err)
	}
	nodes, err := alice.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org})
	if err != nil || len(nodes.GetNodes()) != 1 || !nodes.GetNodes()[0].GetConnected() || nodes.GetNodes()[0].GetShortId() != "abcd1234" {
		t.Fatalf("owner's nodes: %v, %v", nodes, err)
	}
	if n := nodes.GetNodes()[0]; n.GetArch() != "arm64" || n.GetCpus() != 4 || n.GetMemoryBytes() != 8<<30 {
		t.Errorf("node facts: %v", n)
	}
	servers, err := alice.orgs.ListServers(ctx, &panelv1.ListServersRequest{OrgId: org, NodeId: nodeID})
	if err != nil || len(servers.GetServers()) != 2 || servers.GetServers()[0].GetPermissions()[0] != "*" {
		t.Fatalf("owner's servers: %v, %v", servers, err)
	}
	if s := servers.GetServers()[0]; !slices.Equal(s.GetPorts(), []int32{25565, 25566}) || s.GetInstallState() != "installed" {
		t.Errorf("ports %v (want the primary first), install %q", s.GetPorts(), s.GetInstallState())
	}
	servers, err = bob.orgs.ListServers(ctx, &panelv1.ListServersRequest{OrgId: org, NodeId: nodeID})
	if err != nil || len(servers.GetServers()) != 1 || servers.GetServers()[0].GetId() != "s1" ||
		strings.Join(servers.GetServers()[0].GetPermissions(), ",") != "files.read,power" {
		t.Fatalf("member's servers: %v, %v", servers, err)
	}
	if _, err := dave.orgs.ListNodes(ctx, &panelv1.ListNodesRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("outsider listing nodes: %v", err)
	}

	// The grant allows exactly what it says, on exactly that server.
	if _, err := run(bob, "server.start", "s1"); err != nil {
		t.Errorf("member with power: %v", err)
	}
	if _, err := run(bob, "files.read", "s1"); err != nil {
		t.Errorf("member with files.read: %v", err)
	}
	for _, c := range []struct{ action, server string }{
		{"server.command", "s1"}, {"files.write", "s1"}, {"server.delete", "s1"}, {"node.update", ""}, {"server.start", "s2"},
	} {
		if _, err := run(bob, c.action, c.server); connect.CodeOf(err) != connect.CodePermissionDenied && connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("member %s on %q: %v", c.action, c.server, err)
		}
	}
	if _, err := run(alice, "server.everything", "s1"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("unknown action: %v", err)
	}

	// Signed commands: the browser's ID and expiry go to the node as signed.
	id, _ := uuid.NewV7()
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := &panelv1.CommandSignature{CredentialId: []byte("cred"), AuthenticatorData: []byte("ad"), ClientDataJson: []byte("{}"), Signature: []byte("sig")}
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "server.delete", ServerId: "s2", CommandId: id.String(), ExpiresAt: exp, Signature: sig}); err != nil {
		t.Fatal(err)
	}
	if e := node.last(); e.CommandID != id.String() || e.ExpiresAt != exp || e.Signature == nil || string(e.Signature.Signature) != "sig" {
		t.Errorf("signed envelope: %+v", e)
	}
	for what, req := range map[string]*panelv1.ExecuteRequest{
		"signature without its ID": {NodeId: nodeID, Action: "server.delete", ServerId: "s2", Signature: sig},
		"expiry too far":           {NodeId: nodeID, Action: "server.delete", ServerId: "s2", CommandId: id.String(), ExpiresAt: time.Now().Add(time.Hour).Unix()},
		"expired":                  {NodeId: nodeID, Action: "server.delete", ServerId: "s2", CommandId: id.String(), ExpiresAt: time.Now().Add(-time.Minute).Unix()},
		"not a UUIDv7":             {NodeId: nodeID, Action: "server.delete", ServerId: "s2", CommandId: uuid.NewString(), ExpiresAt: exp},
		"params not JSON":          {NodeId: nodeID, Action: "server.start", ServerId: "s1", ParamsJson: "{"},
	} {
		if _, err := alice.cmds.Execute(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", what, err)
		}
	}

	// A command that runs and fails says why.
	node.fail = "the server is already running"
	if _, err := run(alice, "server.start", "s1"); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "already running") {
		t.Errorf("failed command: %v", err)
	}

	// Changes are in the org's log; reads aren't.
	log, err := alice.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	for _, ev := range log.GetEvents() {
		var m map[string]any
		_ = json.Unmarshal([]byte(ev.GetMetadataJson()), &m)
		if ev.GetAction() == "command" {
			logged = append(logged, m["action"].(string))
		} else {
			logged = append(logged, ev.GetAction())
		}
	}
	if got := strings.Join(logged, " "); got != "server.start server.delete server.start access.set server.start org.create" {
		t.Errorf("log: %s", got)
	}

	// Saving a file from the editor carries it whole (Wings allows 4 MiB);
	// every other command's params stay small.
	big := `{"path":"a.txt","data":"` + strings.Repeat("A", 3<<20) + `"}`
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "files.write", ServerId: "s1", ParamsJson: big}); err != nil {
		t.Errorf("a 3 MiB file save: %v", err)
	}
	if _, err := alice.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "files.mkdir", ServerId: "s1", ParamsJson: big}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("3 MiB of params for another action: %v", err)
	}

	// A server's settings (which can hold secrets) go to those who may
	// change them: owners and admins, and members with startup.
	got, err := alice.orgs.GetServer(ctx, &panelv1.GetServerRequest{OrgId: org, NodeId: nodeID, ServerId: "s1"})
	if err != nil || !strings.Contains(got.GetConfigJson(), `"allocations"`) || got.GetServer().GetName() != "mc" {
		t.Errorf("owner's GetServer: %v, %v", got, err)
	}
	got, err = bob.orgs.GetServer(ctx, &panelv1.GetServerRequest{OrgId: org, NodeId: nodeID, ServerId: "s1"})
	if err != nil || got.GetConfigJson() != "" || got.GetServer().GetId() != "s1" {
		t.Errorf("member without startup: %v, %v", got, err)
	}
	if _, err := bob.orgs.GetServer(ctx, &panelv1.GetServerRequest{OrgId: org, NodeId: nodeID, ServerId: "s2"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a server the member can't see: %v", err)
	}

	// Removing access, or the member, takes it away.
	if err := set(alice, "s1", bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := run(bob, "server.start", "s1"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("after removing access: %v", err)
	}
	if err := set(alice, "s1", bobID, "power"); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: bobID}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(bob, "server.start", "s1"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("after leaving the org: %v", err)
	}
	var grants int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM server_grants").Scan(&grants)
	if grants != 0 {
		t.Errorf("%d grants left after the member left", grants)
	}
}
