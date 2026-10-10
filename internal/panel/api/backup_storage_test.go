package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/storage"
	"github.com/xena-studios/raptor/internal/shared/hosted"
)

// Raptor Backup Storage's org calls: admins only, and off (with the price
// still shown) on a Panel without its B2 settings.
func TestBackupStorageAPI(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Storage: &storage.Service{DB: db}}
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
	created, _ := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, org, bobID); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'bs123456', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}

	got, err := alice.orgs.GetBackupStorage(ctx, &panelv1.GetBackupStorageRequest{OrgId: org})
	if err != nil || got.GetAvailable() || got.GetIncludedBytes() != hosted.IncludedBytesPerNode || got.GetCentsPerTb() != 1200 {
		t.Fatalf("get: %v, %v", got, err)
	}
	if _, err := alice.orgs.EnableBackupStorage(ctx, &panelv1.EnableBackupStorageRequest{OrgId: org, NodeId: node}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("enable on a Panel without it: %v", err)
	}
	if _, err := bob.orgs.GetBackupStorage(ctx, &panelv1.GetBackupStorageRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a member: %v", err)
	}
	if _, err := bob.orgs.DisableBackupStorage(ctx, &panelv1.DisableBackupStorageRequest{OrgId: org, NodeId: node}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a member turning it off: %v", err)
	}
	if _, err := alice.orgs.DisableBackupStorage(ctx, &panelv1.DisableBackupStorageRequest{OrgId: org, NodeId: node}); err != nil {
		t.Fatalf("turning off what's off: %v", err)
	}
}
