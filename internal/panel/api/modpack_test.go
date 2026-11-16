package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The pack picker searches Modrinth's modpacks and lists a pack's
// versions without naming a loader: the pack brings its own.
func TestModpackPicker(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	var facets, loaders string
	mr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			facets = r.URL.Query().Get("facets")
			_, _ = io.WriteString(w, `{"hits":[{"project_id":"P1","slug":"adrenaserver","title":"Adrenaserver","project_type":"modpack"}],"total_hits":1}`)
		case "/project/adrenaserver/version":
			loaders = r.URL.Query().Get("loaders")
			_, _ = io.WriteString(w, `[{"id":"V1","project_id":"P1","version_number":"1.7.0+1.21.1","game_versions":["1.21.1"],"loaders":["fabric"],
				"files":[{"url":"https://cdn.modrinth.com/data/P1/x.mrpack","filename":"x.mrpack","primary":true,"hashes":{"sha512":"aa"}}]}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mr.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Modrinth: &modrinth.Client{Base: mr.URL, UserAgent: "test"},
	}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code}}}); err != nil {
		t.Fatal(err)
	}
	created, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()

	res, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "modpacks", Query: "adrena"})
	if err != nil || len(res.GetProjects()) != 1 || facets != `[["project_type:modpack"]]` {
		t.Fatalf("search: %v, %v (facets %s)", res, err, facets)
	}
	vs, err := b.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "adrenaserver", Kind: "modpacks"})
	if err != nil || len(vs.GetVersions()) != 1 || vs.GetVersions()[0].GetId() != "V1" || loaders != "" {
		t.Fatalf("versions: %v, %v (loaders %q)", vs, err, loaders)
	}
	// Add-ons still need their loaders; packs come from Modrinth only.
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "mods"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("mods without loaders: %v", err)
	}
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "modpacks", Source: "hangar"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("packs from Hangar: %v", err)
	}
}
