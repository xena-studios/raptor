package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/crypto/ssh"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

func newSSHKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " me@laptop"
}

func TestSFTPLogin(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	var now time.Time
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}}
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
	add := func(b *browser, line string) (*panelv1.AddSSHKeyResponse, error) {
		return b.auth.AddSSHKey(ctx, &panelv1.AddSSHKeyRequest{PublicKey: line})
	}

	alice, _ := signUp("alice@example.com")
	aliceKey, aliceLine := newSSHKey(t)
	res, err := add(alice, aliceLine)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetSftpUsername() != "alice" || res.GetKey().GetName() != "me@laptop" || res.GetKey().GetFingerprint() != ssh.FingerprintSHA256(aliceKey) {
		t.Errorf("added: %v", res)
	}
	if !strings.Contains(mail.lastMail(t), "SSH key") {
		t.Errorf("no notice: %q", mail.lastMail(t))
	}
	if _, err := add(alice, aliceLine); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("same key twice: %v", err)
	}
	// Keys SFTP won't take.
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 1024)
	weak, _ := ssh.NewPublicKey(&rsaKey.PublicKey)
	for what, line := range map[string]string{
		"not a key":    "hello",
		"1024-bit RSA": string(ssh.MarshalAuthorizedKey(weak)),
		"with options": `command="rm -rf /" ` + aliceLine,
	} {
		if _, err := add(alice, line); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", what, err)
		}
	}
	// Another alice gets a different username.
	alice2, _ := signUp("alice@other.example")
	_, line2 := newSSHKey(t)
	if r, err := add(alice2, line2); err != nil || r.GetSftpUsername() == "alice" || !strings.HasPrefix(r.GetSftpUsername(), "alice") {
		t.Errorf("second alice: %v, %v", r, err)
	}

	// The org, a node, and its servers.
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'abcd1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"s1", "s2"} {
		if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, $2, 'mc', 1, now(), now())`, node, s); err != nil {
			t.Fatal(err)
		}
	}
	login := func(user, server string, key ssh.PublicKey) (*nodev1.SFTPLoginResponse, error) {
		return authSvc.SFTPLogin(ctx, node, &nodev1.SFTPLoginRequest{Username: user, ServerId: server, PublicKey: key.Marshal()})
	}

	// Owners: everything, on every server.
	r, err := login("alice", "s1", aliceKey)
	if err != nil || strings.Join(r.GetPermissions(), ",") != "sftp,files.read,files.write" {
		t.Fatalf("owner: %v, %v", r, err)
	}
	if _, err := login("Alice", "s2", aliceKey); err != nil {
		t.Errorf("usernames are case-insensitive: %v", err)
	}
	// Someone else's key, or a key under someone else's name.
	otherKey, _ := newSSHKey(t)
	if _, err := login("alice", "s1", otherKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("unknown key: %v", err)
	}
	// Not in the org: their own key doesn't help.
	bob, bobID := signUp("bob@example.com")
	bobKey, bobLine := newSSHKey(t)
	if _, err := add(bob, bobLine); err != nil {
		t.Fatal(err)
	}
	if _, err := login("bob", "s1", bobKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("outsider: %v", err)
	}
	// A member: only with the sftp permission on that server, and only
	// their file permissions.
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := login("bob", "s1", bobKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member without a grant: %v", err)
	}
	grant := func(perms ...string) {
		t.Helper()
		if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: node, ServerId: "s1", UserId: bobID, Permissions: perms}); err != nil {
			t.Fatal(err)
		}
	}
	grant("files.read", "power")
	if _, err := login("bob", "s1", bobKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member without sftp: %v", err)
	}
	grant("sftp", "files.read", "power")
	if r, err := login("bob", "s1", bobKey); err != nil || strings.Join(r.GetPermissions(), ",") != "sftp,files.read" || r.GetUserId() != bobID {
		t.Errorf("member with sftp and files.read: %v, %v", r, err)
	}
	if _, err := login("bob", "s2", bobKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member on another server: %v", err)
	}
	// Another node can't ask about this one's servers: the node ID is the
	// connection's, not the request's (nodes.Hub), and its org isn't hers.
	if _, err := authSvc.SFTPLogin(ctx, "01a10f2a-865c-74ec-9578-3fe2ea243894", &nodev1.SFTPLoginRequest{Username: "alice", ServerId: "s1", PublicKey: aliceKey.Marshal()}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("unknown node: %v", err)
	}

	// Keys come and go with a recent re-auth.
	keys, err := alice.auth.ListSSHKeys(ctx, &panelv1.ListSSHKeysRequest{})
	if err != nil || len(keys.GetKeys()) != 1 || keys.GetKeys()[0].GetLastUsedAt() == nil || keys.GetSftpUsername() != "alice" {
		t.Fatalf("list: %v, %v", keys, err)
	}
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	if _, err := alice.auth.DeleteSSHKey(ctx, &panelv1.DeleteSSHKeyRequest{Id: keys.GetKeys()[0].GetId()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("delete without re-auth: %v", err)
	}
	_, line3 := newSSHKey(t)
	if _, err := add(alice, line3); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("add without re-auth: %v", err)
	}
	now = time.Time{}
	if _, err := alice.auth.DeleteSSHKey(ctx, &panelv1.DeleteSSHKeyRequest{Id: keys.GetKeys()[0].GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := login("alice", "s1", aliceKey); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("deleted key: %v", err)
	}
}
