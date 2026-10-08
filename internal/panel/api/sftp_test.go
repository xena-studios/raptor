package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Temporary passwords: made per server, good until they run out or are
// revoked, and only while the user may still use SFTP there.
func TestSFTPPasswords(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
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
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, sftp_enabled, sftp_port, sftp_host_key) VALUES ($1, 'box', 'abcd1234', decode(repeat('00', 32), 'hex'), true, 2022, 'SHA256:host') RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	server := "0192f0a4-0000-7000-8000-0000abcdef12"
	for _, s := range []string{server, "s2"} {
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, $2, 'mc', 1, now(), now())`, node, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	ids := func(srv string) (string, string, string) { return org, node, srv }
	create := func(b *browser, srv string, ttl time.Duration) (*panelv1.CreateSFTPAccessResponse, error) {
		o, n, s := ids(srv)
		return b.orgs.CreateSFTPAccess(ctx, &panelv1.CreateSFTPAccessRequest{OrgId: o, NodeId: n, ServerId: s, TtlSeconds: int64(ttl.Seconds())})
	}
	login := func(username, srv, password string) (*nodev1.SFTPLoginResponse, error) {
		user, _, _ := strings.Cut(username, ".")
		return authSvc.SFTPLogin(ctx, node, &nodev1.SFTPLoginRequest{Username: user, ServerId: srv, Password: password})
	}

	res, err := create(alice, server, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := res.GetAccess()
	if a.GetHost() != "n-abcd1234.raptornodes.net" || a.GetPort() != 2022 || !strings.HasSuffix(a.GetUsername(), ".abcdef12") ||
		len(res.GetPassword()) != 27 || a.GetHostKeyFingerprint() != "SHA256:host" {
		t.Errorf("access: %v, password %q", a, res.GetPassword())
	}
	if left := time.Until(a.GetExpiresAt().AsTime()); left < 23*time.Hour || left > 25*time.Hour {
		t.Errorf("expires in %s, want a day", left)
	}
	got, err := alice.orgs.GetSFTPAccess(ctx, &panelv1.GetSFTPAccessRequest{OrgId: org, NodeId: node, ServerId: server})
	if err != nil || got.GetAccess().GetUsername() != a.GetUsername() {
		t.Errorf("get: %v, %v", got, err)
	}
	r, err := login(a.GetUsername(), server, res.GetPassword())
	if err != nil || strings.Join(r.GetPermissions(), ",") != "sftp,files.read,files.write" || r.GetExpiresAt() == nil {
		t.Fatalf("login: %v, %v", r, err)
	}
	for what, c := range map[string][3]string{
		"wrong password":  {a.GetUsername(), server, "nope"},
		"another server":  {a.GetUsername(), "s2", res.GetPassword()},
		"unknown user":    {"t-aaaaaaaaaa.x", server, res.GetPassword()},
		"a key user name": {"alice.x", server, res.GetPassword()},
	} {
		if _, err := login(c[0], c[1], c[2]); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: %v", what, err)
		}
	}
	if _, err := create(alice, server, 10*time.Minute); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("too short: %v", err)
	}
	if _, err := create(alice, server, 31*24*time.Hour); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("too long: %v", err)
	}

	// Making a new one replaces the old.
	again, err := create(alice, server, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := login(a.GetUsername(), server, res.GetPassword()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("the replaced password: %v", err)
	}
	if _, err := login(again.GetAccess().GetUsername(), server, again.GetPassword()); err != nil {
		t.Errorf("the new password: %v", err)
	}

	// Members need the sftp permission, to make one and to use it.
	if _, err := create(bob, server, 0); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("member without access: %v", err)
	}
	grant := func(perms ...string) {
		t.Helper()
		if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: node, ServerId: server, UserId: bobID, Permissions: perms}); err != nil {
			t.Fatal(err)
		}
	}
	grant("files.read")
	if _, err := create(bob, server, 0); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member without sftp: %v", err)
	}
	grant("sftp", "files.read")
	bobs, err := create(bob, server, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := login(bobs.GetAccess().GetUsername(), server, bobs.GetPassword()); err != nil || strings.Join(r.GetPermissions(), ",") != "sftp,files.read" {
		t.Errorf("member's login: %v, %v", r, err)
	}
	grant("files.read")
	if _, err := login(bobs.GetAccess().GetUsername(), server, bobs.GetPassword()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("after losing sftp: %v", err)
	}

	// Revoked, or run out: no more logins.
	rev, err := alice.orgs.RevokeSFTPAccess(ctx, &panelv1.RevokeSFTPAccessRequest{OrgId: org, NodeId: node, ServerId: server})
	if err != nil || rev.GetUsername() == "" || !strings.HasPrefix(again.GetAccess().GetUsername(), rev.GetUsername()+".") {
		t.Errorf("revoke: %v, %v", rev, err)
	}
	if _, err := login(again.GetAccess().GetUsername(), server, again.GetPassword()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("revoked: %v", err)
	}
	if got, _ := alice.orgs.GetSFTPAccess(ctx, &panelv1.GetSFTPAccessRequest{OrgId: org, NodeId: node, ServerId: server}); got.GetAccess() != nil {
		t.Errorf("still listed after revoking: %v", got)
	}
	last, err := create(alice, server, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE sftp_passwords SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := login(last.GetAccess().GetUsername(), server, last.GetPassword()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("expired: %v", err)
	}
}
