package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/demo"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// fakeTurnstile passes only the token "ok".
type fakeTurnstile struct{}

func (fakeTurnstile) Verify(_ context.Context, token, _ string) error {
	if token != "ok" {
		return errors.New("bad token")
	}
	return nil
}

func offInDemo(err error) bool {
	return connect.CodeOf(err) == connect.CodePermissionDenied && strings.Contains(err.Error(), "off in the demo")
}

// The public demo, Phase 1 (#449): guests start behind Turnstile, land in
// the demo org with its showcase servers' grants, and the Panel refuses
// each thing on the deny list to them, while the same things work in a
// normal org. The hourly reset and the janitor remove guests.
func TestDemo(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	node := &fakeNode{t: t, panelKey: pub}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Turnstile: fakeTurnstile{}}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: orgSvc,
		Commands: &commands.Service{Auth: authSvc, Sender: node, PanelKey: key}, AppOrigin: appOrigin,
	}))
	defer srv.Close()

	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email, TurnstileToken: "ok"}); err != nil {
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
	newOrg := func(b *browser, name string) (org, nodeID string) {
		t.Helper()
		created, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		org = created.GetOrg().GetId()
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, config) VALUES ($1, 's1', 'mc', 1, now(), now(), '{}')`, nodeID); err != nil {
			t.Fatal(err)
		}
		return org, nodeID
	}

	// Closed until staff make an org the demo.
	visitor := newBrowser(t, srv, appOrigin)
	if res, err := visitor.auth.GetDemo(ctx, &panelv1.GetDemoRequest{}); err != nil || res.GetOpen() {
		t.Fatalf("GetDemo with no demo org: %v, %v", res, err)
	}
	if _, err := visitor.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("StartDemo with no demo org: %v", err)
	}

	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	demoOrg, demoNode := newOrg(alice, "Raptor demo")
	acme, acmeNode := newOrg(alice, "Acme")
	// Bob is a member of the normal org with what guests get, and more.
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", acme, bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions) VALUES ($1, $2, $3, 's1',
		ARRAY['console.write', 'power', 'files.read', 'files.write', 'backups', 'schedules', 'startup', 'players', 'sftp'])`, acme, bobID, acmeNode); err != nil {
		t.Fatal(err)
	}

	// Only staff, from the Panel's server, make an org the demo.
	var out bytes.Buffer
	for _, args := range [][]string{{"demo", "on", demoOrg}, {"demo", "server", demoOrg, demoNode, "s1"}, {"demo", "backup", demoNode, "s1", "b-good"}, {"demo", "status"}} {
		if err := staff.CLI(ctx, db, &out, args); err != nil {
			t.Fatalf("panel staff %v: %v", args, err)
		}
	}
	if err := staff.CLI(ctx, db, &out, []string{"demo", "server", demoOrg, demoNode, "s1", "sftp"}); err == nil {
		t.Error("guests were given SFTP")
	}
	if err := staff.CLI(ctx, db, &out, []string{"demo", "server", demoOrg, acmeNode, "s1"}); err == nil {
		t.Error("another org's node became a showcase server")
	}
	if res, err := visitor.auth.GetDemo(ctx, &panelv1.GetDemoRequest{}); err != nil || !res.GetOpen() {
		t.Fatalf("GetDemo: %v, %v", res, err)
	}

	// Turnstile first; a signed-in browser isn't swapped for a guest.
	if _, err := visitor.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "bad"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("StartDemo past Turnstile: %v", err)
	}
	if _, err := alice.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("StartDemo signed in: %v", err)
	}
	started, err := visitor.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	guest := visitor
	if started.GetOrgId() != demoOrg || started.GetUser().GetGuestUntil() == nil {
		t.Fatalf("StartDemo: %v", started)
	}
	if left := time.Until(started.GetUser().GetGuestUntil().AsTime()); left <= 50*time.Minute || left > auth.GuestTTL {
		t.Errorf("guest lasts %s", left)
	}
	if !strings.HasSuffix(started.GetUser().GetEmail(), "@"+auth.GuestDomain) {
		t.Errorf("guest email %q", started.GetUser().GetEmail())
	}
	sess, err := guest.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if err != nil || sess.GetUser().GetGuestUntil() == nil {
		t.Fatalf("guest session: %v, %v", sess, err)
	}
	list, err := guest.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{})
	if err != nil || len(list.GetOrgs()) != 1 || !list.GetOrgs()[0].GetDemo() || list.GetOrgs()[0].GetRole() != panelv1.Role_ROLE_MEMBER {
		t.Fatalf("guest's orgs: %v, %v", list, err)
	}

	run := func(b *browser, nodeID, action, params string, signed bool) error {
		req := &panelv1.ExecuteRequest{NodeId: nodeID, Action: action, ServerId: "s1", ParamsJson: params}
		if signed {
			id, _ := uuid.NewV7()
			req.CommandId, req.ExpiresAt = id.String(), time.Now().Add(time.Minute).Unix()
			req.Signature = &panelv1.CommandSignature{CredentialId: []byte{1}, AuthenticatorData: []byte{1}, ClientDataJson: []byte("{}"), Signature: []byte{1}}
		}
		_, err := b.cmds.Execute(ctx, req)
		return err
	}
	// What guests are there to try.
	for _, c := range []struct{ action, params string }{
		{"server.command", `{"command":"say hi"}`},
		{"server.start", `{}`},
		{"files.list", `{"dir":"/"}`},
		{"files.write", `{"path":"server.properties","content":""}`},
		{"addons.install", `{}`},
		{"backup.create", `{}`},
		{"schedule.run", `{}`},
		{"server.update", `{"memory_mb":2048}`},
	} {
		if err := run(guest, demoNode, c.action, c.params, false); err != nil {
			t.Errorf("guest %s: %v", c.action, err)
		}
	}
	// The deny list: refused to the guest in the demo, by the Panel, and
	// not refused for that reason in a normal org (to its owner or a
	// member with the grant).
	for _, c := range []struct {
		action, params string
		signed, member bool // member: a member with the grant can in a normal org
	}{
		{action: "server.create", params: `{}`, signed: true},
		{action: "server.delete", params: `{}`, signed: true},
		{action: "server.reinstall", params: `{"wipe":true}`, signed: true},
		{action: "server.update", params: `{"image":"x"}`, signed: true, member: true},
		{action: "backup.restore", params: `{"backup_id":"b1"}`, signed: true, member: true},
		{action: "files.pull", params: `{"url":"https://example.com/x"}`, member: true},
		{action: "files.import", params: `{}`, member: true},
		{action: "sftp.disconnect", params: `{}`, member: true},
		{action: "node.db.instance.external", params: `{}`},
		{action: "node.network.save", params: `{}`},
		{action: "server.networks", params: `{}`},
		{action: "node.firewall", params: `{}`},
		{action: "node.hostname", params: `{}`},
		{action: "support.grant", params: `{}`},
	} {
		if err := run(guest, demoNode, c.action, c.params, c.signed); !offInDemo(err) {
			t.Errorf("guest %s in the demo: %v", c.action, err)
		}
		// support.grant needs a staff request first: refused, but not
		// for being the demo.
		if err := run(alice, acmeNode, c.action, c.params, c.signed); err != nil && (c.action != "support.grant" || offInDemo(err)) {
			t.Errorf("owner %s in a normal org: %v", c.action, err)
		}
		if c.member {
			if err := run(bob, acmeNode, c.action, c.params, c.signed); err != nil {
				t.Errorf("member %s in a normal org: %v", c.action, err)
			}
		}
	}
	// The demo org's owners (staff) aren't under it: they set the demo up.
	if err := run(alice, demoNode, "server.create", `{}`, true); err != nil {
		t.Errorf("demo owner server.create: %v", err)
	}

	// The org's RPCs on the deny list, and the account changes a guest
	// isn't for, against the same calls in a normal org.
	for name, call := range map[string]func(b *browser, org, nodeID string) error{
		"SFTP": func(b *browser, org, nodeID string) error {
			_, err := b.orgs.CreateSFTPAccess(ctx, &panelv1.CreateSFTPAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1"})
			return err
		},
		"share a log": func(b *browser, org, nodeID string) error {
			_, err := b.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{OrgId: org, NodeId: nodeID, ServerId: "s1", Name: "latest.log", Content: "hi"})
			return err
		},
		"members": func(b *browser, org, _ string) error {
			_, err := b.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org})
			return err
		},
		"invitations": func(b *browser, org, _ string) error {
			_, err := b.orgs.InviteMember(ctx, &panelv1.InviteMemberRequest{OrgId: org, Email: "carol@example.com", Role: panelv1.Role_ROLE_MEMBER})
			return err
		},
		"billing": func(b *browser, org, _ string) error {
			_, err := b.bill.GetBilling(ctx, &panelv1.GetBillingRequest{OrgId: org})
			return err
		},
		"node settings": func(b *browser, org, nodeID string) error {
			_, err := b.orgs.RenameNode(ctx, &panelv1.RenameNodeRequest{OrgId: org, NodeId: nodeID, Name: "box2"})
			return err
		},
		"node SFTP": func(b *browser, org, nodeID string) error {
			_, err := b.orgs.SetNodeSFTP(ctx, &panelv1.SetNodeSFTPRequest{OrgId: org, NodeId: nodeID, Allowed: true})
			return err
		},
		"support access": func(b *browser, org, _ string) error {
			_, err := b.orgs.DenySupportRequest(ctx, &panelv1.DenySupportRequestRequest{OrgId: org, RequestId: uuid.NewString()})
			return err
		},
		"delete the org": func(b *browser, org, _ string) error {
			// A normal org with a node can't be deleted yet either, for
			// that reason, not this one.
			_, err := b.orgs.DeleteOrg(ctx, &panelv1.DeleteOrgRequest{OrgId: org})
			return err
		},
		"make an org": func(b *browser, _, _ string) error {
			_, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Mine"})
			return err
		},
		"rename yourself": func(b *browser, _, _ string) error {
			_, err := b.auth.UpdateProfile(ctx, &panelv1.UpdateProfileRequest{Name: new("Someone")})
			return err
		},
		"add an email": func(b *browser, _, _ string) error {
			_, err := b.auth.StartEmailChange(ctx, &panelv1.StartEmailChangeRequest{NewEmail: "guest@example.com"})
			return err
		},
	} {
		if err := call(guest, demoOrg, demoNode); !offInDemo(err) {
			t.Errorf("guest %s: %v", name, err)
		}
		if err := call(alice, acme, acmeNode); offInDemo(err) {
			t.Errorf("owner %s in a normal org: %v", name, err)
		}
	}
	if _, err := bob.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{OrgId: acme, NodeId: acmeNode, ServerId: "s1", Name: "latest.log", Content: "hi"}); offInDemo(err) {
		t.Errorf("member share a log in a normal org: %v", err)
	}
	// What guests may still do outside commands.
	if _, err := guest.orgs.ListServers(ctx, &panelv1.ListServersRequest{OrgId: demoOrg, NodeId: demoNode}); err != nil {
		t.Errorf("guest ListServers: %v", err)
	}

	// The hourly reset removes guests (signing them out) and empties the
	// showcase servers' trash as the Panel; it doesn't restore yet.
	reset := &demo.Reset{DB: db, Sender: node, PanelKey: key}
	res, err := reset.ResetOrg(ctx, pgtype.UUID{Bytes: uuid.MustParse(demoOrg), Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Guests != 1 || res.TrashEmptied != 1 || res.RestoresSkipped != 1 {
		t.Errorf("reset: %+v", res)
	}
	if e := node.last(); e.Action != "files.trash.empty" || e.UserID != demo.UserID || e.ServerID != "s1" || e.Signature != nil {
		t.Errorf("reset sent %+v", e)
	}
	if _, err := guest.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("guest after the reset: %v", err)
	}
	if _, err := alice.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); err != nil {
		t.Errorf("the owner after the reset: %v", err)
	}
	// It runs once an hour.
	if err := reset.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(node.got)
	if err := reset.Tick(ctx); err != nil || len(node.got) != before {
		t.Errorf("a second reset within the hour: %v, %d commands", err, len(node.got)-before)
	}

	// The janitor removes guests whose hour is up.
	late := newBrowser(t, srv, appOrigin)
	if _, err := late.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE users SET guest_until = now() - interval '1 minute' WHERE guest_until IS NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := late.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an expired guest: %v", err)
	}
	if err := authSvc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var guests int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users WHERE guest_until IS NOT NULL").Scan(&guests); err != nil || guests != 0 {
		t.Errorf("guests after pruning: %d, %v", guests, err)
	}

	// Per address: a script can't fill the demo.
	limited := false
	for range 12 {
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); connect.CodeOf(err) == connect.CodeResourceExhausted {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("StartDemo isn't limited per address")
	}
	// No email ever went to a guest.
	for _, m := range mail.mail {
		if to, _, _ := strings.Cut(m, "\n"); strings.HasSuffix(to, ".invalid") {
			t.Errorf("emailed a guest: %s", to)
		}
	}
}
