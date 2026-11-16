package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/factorio"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Factorio's mods: search from the portal's list, releases for the
// server's Factorio version with their SHA-1s, installed files named by
// file name, and dependencies resolved by mod name.
func TestFactorioAddons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	s1, s2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	rel := func(mod, version, fv, sum, deps string) string {
		return `{"download_url":"/download/` + mod + `/x` + version + `","file_name":"` + mod + `_` + version + `.zip","version":"` + version + `","sha1":"` + sum + `",
			"released_at":"2026-0` + version[len(version)-1:] + `-01T00:00:00Z","info_json":{"factorio_version":"` + fv + `","dependencies":[` + deps + `]}}`
	}
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/mods":
			_, _ = io.WriteString(w, `{"results":[{"name":"Krastorio2","title":"Krastorio 2","owner":"raiguard","downloads_count":90,
				"latest_release":`+rel("Krastorio2", "2.1.4", "2.1", s2, "")+`}]}`)
		case "/api/mods/Krastorio2/full":
			_, _ = io.WriteString(w, `{"name":"Krastorio2","title":"Krastorio 2","thumbnail":"/assets/k2.thumb.png","releases":[`+
				rel("Krastorio2", "2.1.3", "2.1", s1, `"base >= 2.1", "flib >= 0.16", "? Aircraft", "! KS_Power"`)+`,`+
				rel("Krastorio2", "2.1.4", "2.1", s2, `"base >= 2.1", "flib >= 0.16", "? Aircraft", "! KS_Power"`)+`]}`)
		case "/api/mods/flib/full":
			_, _ = io.WriteString(w, `{"name":"flib","title":"Factorio Library","releases":[`+rel("flib", "0.16.2", "2.1", s1, `"base"`)+`]}`)
		case "/api/mods/Aircraft/full":
			_, _ = io.WriteString(w, `{"name":"Aircraft","releases":[`+rel("Aircraft", "1.6.6", "2.0", s1, "")+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer portal.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Factorio: &factorio.Client{Base: portal.URL, UserAgent: "test"},
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
	loaders := []string{"factorio"}

	res, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "factorio", Loaders: loaders, Source: "factorio", Query: "krast", GameVersion: "2.1.10"})
	if err != nil || len(res.GetProjects()) != 1 || res.GetProjects()[0].GetUrl() != "https://mods.factorio.com/mod/Krastorio2" {
		t.Fatalf("search: %v, %v", res, err)
	}
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "factorio", Loaders: loaders}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("Factorio's mods from Modrinth: %v", err)
	}
	vs, err := b.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "Krastorio2", Loaders: loaders, Source: "factorio", GameVersion: "2.1.10"})
	if err != nil || len(vs.GetVersions()) != 2 {
		t.Fatalf("versions: %v, %v", vs, err)
	}
	newest := vs.GetVersions()[0]
	if newest.GetId() != "Krastorio2@2.1.4" || newest.GetFile().GetSha1() != s2 || newest.GetFile().GetUrl() != "https://mods.factorio.com/download/Krastorio2/x2.1.4" || len(newest.GetDependencies()) != 3 {
		t.Errorf("newest: %v", newest)
	}

	h1, h2 := strings.Repeat("a", 128), strings.Repeat("b", 128)
	id, err := b.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{
		OrgId: org, Loaders: loaders, GameVersion: "2.1.10", Hashes: []string{h1, h2},
		Names: map[string]string{h1: "Krastorio2_2.1.3.zip", h2: "mod-list.json"},
		Sha1:  map[string]string{h1: s1},
	})
	if err != nil || len(id.GetAddons()) != 1 {
		t.Fatalf("identify: %v, %v", id, err)
	}
	if a := id.GetAddons()[0]; a.GetVersion().GetVersionNumber() != "2.1.3" || a.GetLatest().GetVersionNumber() != "2.1.4" || a.GetProject().GetIconUrl() != factorio.AssetsBase+"/assets/k2.thumb.png" {
		t.Errorf("an older Krastorio 2: %v", a)
	}

	deps, err := b.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, Source: "factorio", VersionId: "Krastorio2@2.1.4", Loaders: loaders, GameVersion: "2.1.10", Installed: []string{"KS_Power"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deps.GetRequired()) != 1 || deps.GetRequired()[0].GetProject().GetId() != "flib" {
		t.Errorf("required: %v", deps.GetRequired())
	}
	// Aircraft has no release for 2.1, so it isn't offered.
	if len(deps.GetOptional()) != 0 {
		t.Errorf("optional: %v", deps.GetOptional())
	}
	if len(deps.GetIncompatible()) != 1 || deps.GetIncompatible()[0].GetId() != "KS_Power" {
		t.Errorf("incompatible: %v", deps.GetIncompatible())
	}
}
