package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/images"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Eggs' icons: the one in the egg's file, or one an admin uploads, which
// the file's never replaces (docs/PANEL.md#images).
func TestEggIcons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: func() time.Time { return now }}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Staff: &staff.Service{DB: db, Auth: authSvc}, Images: images.Handler(db), AppOrigin: appOrigin}))
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

	// The catalog's test egg has its icon.png in the official library.
	if _, _, _, err := catalog.Seed(ctx, db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	var official int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM eggs WHERE catalog_id = 'generic/raptor-test' AND icon IS NOT NULL AND icon_source = 'file'`).Scan(&official); err != nil || official != 1 {
		t.Errorf("the test egg's icon: %d (%v)", official, err)
	}
	// Eggs carrying their own icon: Pelican's, as data: URIs.
	var withIcon [][]byte
	for _, f := range []string{"python.plcn_v3.json", "neoforge.plcn_v3.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "eggs", "testdata", f)) //nolint:gosec // test fixtures
		if err != nil {
			t.Fatal(err)
		}
		egg, err := eggs.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := images.FromDataURI(egg.Icon); !ok || err != nil {
			t.Fatalf("%s has no icon: %v", f, err)
		}
		withIcon = append(withIcon, b)
	}

	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, org, bobID)
	icon := func(id string) (string, bool) {
		t.Helper()
		d, err := alice.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return d.GetEgg().GetIcon(), d.GetEgg().GetIconUploaded()
	}

	// An imported egg shows the icon in its file.
	imported, err := alice.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: withIcon[0], Category: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	id := imported.GetId()
	fileIcon, uploaded := icon(id)
	if !images.ValidID(fileIcon) || uploaded {
		t.Fatalf("the file's icon: %q uploaded %v", fileIcon, uploaded)
	}
	list, err := bob.lib.ListEggs(ctx, &panelv1.EggServiceListEggsRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.GetEggs() {
		if e.GetId() == id && e.GetIcon() != fileIcon {
			t.Errorf("listed with icon %q", e.GetIcon())
		}
	}

	// Members can't change it; admins upload one, which stays through edits
	// and goes with a copy.
	if _, err := bob.lib.SetEggIcon(ctx, &panelv1.SetEggIconRequest{OrgId: org, Id: id, Image: testPNG(t, 64, 64, 4)}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member setting an icon: %v", err)
	}
	if _, err := alice.lib.SetEggIcon(ctx, &panelv1.SetEggIconRequest{OrgId: org, Id: id, Image: []byte("<svg/>")}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an SVG icon: %v", err)
	}
	set, err := alice.lib.SetEggIcon(ctx, &panelv1.SetEggIconRequest{OrgId: org, Id: id, Image: testPNG(t, 64, 64, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if got, up := icon(id); got != set.GetIcon() || got == fileIcon || !up {
		t.Errorf("uploaded: %q (%v), response %q", got, up, set.GetIcon())
	}
	d, _ := alice.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: id})
	spec := d.GetSpec()
	spec.Description = "Edited"
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: spec}, Category: "generic"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := icon(id); got != set.GetIcon() {
		t.Errorf("an edit replaced the uploaded icon: %q", got)
	}
	copied, err := alice.lib.CustomizeEgg(ctx, &panelv1.CustomizeEggRequest{OrgId: org, Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if got, up := icon(copied.GetId()); got != set.GetIcon() || !up {
		t.Errorf("the copy's icon: %q %v", got, up)
	}

	// Removing the upload brings back the file's.
	if res, err := alice.lib.SetEggIcon(ctx, &panelv1.SetEggIconRequest{OrgId: org, Id: id}); err != nil || res.GetIcon() != fileIcon {
		t.Errorf("removing the upload: %v %v", res, err)
	}
	if got, up := icon(id); got != fileIcon || up {
		t.Errorf("after removing: %q %v", got, up)
	}

	// An egg without one has none.
	plain, err := alice.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: helloSpec("Plain"), Category: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := icon(plain.GetId()); got != "" {
		t.Errorf("an egg without an icon: %q", got)
	}

	// Eggs saved before icons get theirs.
	exec(`UPDATE eggs SET icon = NULL, icon_source = '' WHERE org_id = $1::uuid AND icon_source = 'file'`, org)
	if n, err := catalog.BackfillIcons(ctx, db); err != nil || n == 0 {
		t.Errorf("backfill: %d %v", n, err)
	}
	if got, _ := icon(id); got != fileIcon {
		t.Errorf("after the backfill: %q", got)
	}

	// The cleanup keeps icons in use.
	exec(`UPDATE images SET created_at = now() - interval '2 days'`)
	if err := authSvc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var kept bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM images WHERE id = $1)`, fileIcon).Scan(&kept); err != nil || !kept {
		t.Errorf("an egg's icon was cleaned up: %v", err)
	}

	// Taken down: gone from every egg, and the file's doesn't come back.
	if n, err := store.New(db).TakeDownImage(ctx, fileIcon); err != nil || n != 1 {
		t.Fatalf("take down: %d %v", n, err)
	}
	if got, _ := icon(id); got != "" {
		t.Errorf("a taken-down icon is still shown: %q", got)
	}
	spec.Description = "Edited again"
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: spec}, Category: "generic"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := icon(id); got != "" {
		t.Errorf("a taken-down icon came back from the file: %q", got)
	}
}

// An egg that says its category (x-raptor.category, as the egg library
// exports them) goes there when imported without one; one chosen wins.
func TestImportCategory(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: "a@example.com", Code: code}}}); err != nil {
		t.Fatal(err)
	}
	created, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	file := []byte(`{"meta":{"version":"PTDL_v2"},"name":"Steamy","docker_images":{"x":"ghcr.io/x/y"},"startup":"./run","x-raptor":{"category":"steam"}}`)
	r, err := b.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_File{File: file}})
	if err != nil || r.GetReview().GetCategory() != "steam" {
		t.Fatalf("review: %q %v", r.GetReview().GetCategory(), err)
	}
	for want, category := range map[string]string{"steam": "", "voice": "voice"} {
		f := append([]byte{}, file...)
		f = append(f[:len(f)-1], []byte(`,"description":"`+want+`"}`)...) // a different file each time
		res, err := b.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: f, Category: category})
		if err != nil {
			t.Fatal(err)
		}
		d, err := b.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: res.GetId()})
		if err != nil || d.GetEgg().GetCategory() != want {
			t.Errorf("imported with %q: category %q, want %q (%v)", category, d.GetEgg().GetCategory(), want, err)
		}
	}
}
