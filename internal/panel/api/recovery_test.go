package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/backupkeys"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

type keyNode struct{}

func (keyNode) Execute(context.Context, string, []byte) (*nodev1.ExecuteResponse, error) {
	b, _ := json.Marshal(map[string]string{"mode": "panel", "key": "old-node-key", "fingerprint": "f00"})
	return &nodev1.ExecuteResponse{Result: b}, nil
}

// Recovering a removed node's backups: owners only, with the Panel's copy of
// its key, and never another org's node.
func TestRecoveryAPI(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	dk := make([]byte, 32)
	_, _ = rand.Read(dk)
	keeper := &backupkeys.Keeper{DB: db, Sender: keyNode{}, PanelKey: key, DataKey: dk}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Keys: keeper}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
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
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	carol, _ := signUp("carol@example.com")
	created, _ := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	org := created.GetOrg().GetId()
	other, _ := carol.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Other"})
	if _, err := db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')`, org, bobID); err != nil {
		t.Fatal(err)
	}
	newNode := func(org, name string, removed bool) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, deleted_at) VALUES ($1, $2, substr(md5(random()::text), 1, 8), decode(repeat('00', 32), 'hex'), CASE WHEN $3 THEN now() END) RETURNING id`, org, name, removed).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dead := newNode(org, "dead-box", true)
	newNode(org, "new-box", false)
	theirs := newNode(other.GetOrg().GetId(), "theirs", false)
	if _, err := keeper.Sync(ctx, uuid.MustParse(org), uuid.MustParse(dead)); err != nil {
		t.Fatal(err)
	}

	list, err := alice.orgs.ListRecoverySources(ctx, &panelv1.ListRecoverySourcesRequest{OrgId: org})
	if err != nil || len(list.GetSources()) != 2 {
		t.Fatalf("sources: %v, %v", list, err)
	}
	got := map[string]*panelv1.RecoverySource{}
	for _, s := range list.GetSources() {
		got[s.GetName()] = s
	}
	if d := got["dead-box"]; d == nil || !d.GetRemoved() || !d.GetKeyKept() || d.GetHasStorage() {
		t.Fatalf("dead box: %v", d)
	}
	p, err := alice.orgs.PrepareRecovery(ctx, &panelv1.PrepareRecoveryRequest{OrgId: org, FromNodeId: dead})
	if err != nil || p.GetKey() != "old-node-key" || p.GetDestinationJson() != "" {
		t.Fatalf("prepare: %v, %v", p, err)
	}
	// An admin isn't an owner.
	if _, err := bob.orgs.PrepareRecovery(ctx, &panelv1.PrepareRecoveryRequest{OrgId: org, FromNodeId: dead}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an admin: %v", err)
	}
	if _, err := bob.orgs.ListRecoverySources(ctx, &panelv1.ListRecoverySourcesRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an admin listing: %v", err)
	}
	// Another org's node, through this org: not found.
	if _, err := alice.orgs.PrepareRecovery(ctx, &panelv1.PrepareRecoveryRequest{OrgId: org, FromNodeId: theirs}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("another org's node: %v", err)
	}
	// Storage on a Panel without it: a plain error.
	if _, err := alice.orgs.PrepareRecovery(ctx, &panelv1.PrepareRecoveryRequest{OrgId: org, FromNodeId: dead, Storage: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("storage without it: %v", err)
	}
	log, _ := alice.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if log.GetEvents()[0].GetAction() != "backup.recover.prepare" {
		t.Fatalf("not audited: %v", log.GetEvents()[0])
	}
}
