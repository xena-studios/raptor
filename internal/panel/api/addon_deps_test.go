package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Resolving a mod's dependencies: required ones (and theirs) first, once
// each, at a version for the server; optional ones only its own; installed
// projects left out, or named when it can't run beside them; and what
// can't be installed said so.
func TestAddonDependencies(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	h := strings.Repeat("a", 128)
	version := func(id, project, deps string) string {
		return `{"id":"` + id + `","project_id":"` + project + `","version_number":"1.0","loaders":["fabric"],"game_versions":["1.21.1"],
			"files":[{"url":"https://cdn.modrinth.com/data/` + project + `/x.jar","filename":"` + project + `.jar","size":1,"primary":true,"hashes":{"sha512":"` + h + `"}}],
			"dependencies":[` + deps + `]}`
	}
	dep := func(project, versionID, typ string) string {
		return `{"project_id":` + quoteOrNull(project) + `,"version_id":` + quoteOrNull(versionID) + `,"dependency_type":"` + typ + `"}`
	}
	var searched string
	mr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			searched = r.URL.Query().Get("facets")
			_, _ = io.WriteString(w, `{"hits":[],"total_hits":0}`)
		case "/version/root":
			_, _ = io.WriteString(w, version("root", "LITH", strings.Join([]string{
				dep("FAPI", "", "required"),
				dep("", "cl1", "required"),
				dep("MMENU", "", "optional"),
				dep("OPTI", "", "incompatible"),
				dep("GONE", "", "required"),
				dep("EMB", "", "embedded"),
				`{"file_name":"external.jar","dependency_type":"required"}`,
			}, ",")))
		case "/version/cl1":
			_, _ = io.WriteString(w, version("cl1", "CLOTH", dep("FAPI", "", "required")+","+dep("DEEPOPT", "", "optional")))
		case "/project/FAPI/version":
			_, _ = io.WriteString(w, `[`+version("fa2", "FAPI", "")+`]`)
		case "/project/MMENU/version":
			_, _ = io.WriteString(w, `[`+version("mm1", "MMENU", "")+`]`)
		case "/project/GONE/version":
			_, _ = io.WriteString(w, `[]`)
		case "/projects":
			_, _ = io.WriteString(w, `[{"id":"FAPI","slug":"fabric-api","title":"Fabric API"},{"id":"CLOTH","slug":"cloth","title":"Cloth Config"},
				{"id":"MMENU","slug":"modmenu","title":"Mod Menu"},{"id":"OPTI","slug":"optifabric","title":"OptiFabric"},{"id":"GONE","slug":"gone","title":"Gone"}]`)
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

	res, err := b.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, VersionId: "root", Loaders: []string{"fabric"}, GameVersion: "1.21.1", Installed: []string{"OPTI"},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := func(list []*panelv1.ResolvedAddon) []string {
		var out []string
		for _, a := range list {
			out = append(out, a.GetProject().GetTitle()+"@"+a.GetVersion().GetId()+"<"+a.GetNeededBy())
		}
		return out
	}
	// Fabric API once, before Cloth Config, which needs it too; Cloth's
	// own optional dependency isn't offered.
	if got := strings.Join(names(res.GetRequired()), " "); got != "Fabric API@fa2<LITH Cloth Config@cl1<LITH" {
		t.Errorf("required: %s", got)
	}
	if got := strings.Join(names(res.GetOptional()), " "); got != "Mod Menu@mm1<LITH" {
		t.Errorf("optional: %s", got)
	}
	if len(res.GetIncompatible()) != 1 || res.GetIncompatible()[0].GetTitle() != "OptiFabric" {
		t.Errorf("incompatible: %v", res.GetIncompatible())
	}
	var unavailable []string
	for _, p := range res.GetUnavailable() {
		unavailable = append(unavailable, p.GetTitle())
	}
	if strings.Join(unavailable, ",") != "Gone,external.jar" {
		t.Errorf("unavailable: %v", unavailable)
	}

	// Installed already: not needed again.
	res, err = b.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, VersionId: "root", Loaders: []string{"fabric"}, GameVersion: "1.21.1", Installed: []string{"FAPI", "MMENU"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(res.GetRequired()), " "); got != "Cloth Config@cl1<LITH" || len(res.GetOptional()) != 0 || len(res.GetIncompatible()) != 0 {
		t.Errorf("with some installed: %s, %v, %v", got, res.GetOptional(), res.GetIncompatible())
	}

	// Search leaves client-only mods out unless asked.
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "mods", Loaders: []string{"fabric"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searched, `["server_side:required","server_side:optional"]`) {
		t.Errorf("search facets: %s", searched)
	}
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "mods", Loaders: []string{"fabric"}, IncludeClientOnly: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(searched, "server_side") {
		t.Errorf("client-only asked for: %s", searched)
	}
}

func quoteOrNull(s string) string {
	if s == "" {
		return "null"
	}
	return `"` + s + `"`
}
