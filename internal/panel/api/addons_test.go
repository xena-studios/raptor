package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The plugin installer's Modrinth lookups: members only, checked, and
// shaped for the app.
func TestAddons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	h1, h2 := strings.Repeat("a", 128), strings.Repeat("b", 128)
	var searched string
	mr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			searched = r.URL.Query().Get("facets")
			_, _ = io.WriteString(w, `{"hits":[{"project_id":"Vebnzrzj","slug":"luckperms","title":"LuckPerms","description":"Permissions","downloads":5,"author":"lucko","project_type":"mod"}],"total_hits":1}`)
		case "/project/luckperms/version":
			_, _ = io.WriteString(w, `[{"id":"v2","project_id":"Vebnzrzj","name":"5.5","version_number":"5.5","game_versions":["1.21.1"],"loaders":["paper"],"date_published":"2026-08-01T00:00:00Z",
				"files":[{"url":"https://cdn.modrinth.com/x/LuckPerms.jar","filename":"LuckPerms.jar","size":10,"primary":true,"hashes":{"sha512":"`+h2+`"}}]}]`)
		case "/version_files":
			var body struct{ Hashes []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = io.WriteString(w, `{"`+h1+`":{"id":"v1","project_id":"Vebnzrzj","version_number":"5.4","date_published":"2026-01-01T00:00:00Z","files":[{"filename":"LuckPerms-old.jar","primary":true}]}}`)
		case "/version_files/update":
			_, _ = io.WriteString(w, `{"`+h1+`":{"id":"v2","project_id":"Vebnzrzj","version_number":"5.5","date_published":"2026-08-01T00:00:00Z","files":[{"url":"https://cdn.modrinth.com/x/LuckPerms.jar","filename":"LuckPerms.jar","primary":true,"hashes":{"sha512":"`+h2+`"}}]}}`)
		case "/projects":
			_, _ = io.WriteString(w, `[{"id":"Vebnzrzj","slug":"luckperms","title":"LuckPerms","icon_url":"https://cdn.modrinth.com/i.png"}]`)
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
	signUp := func(email string) *browser {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: email, Code: code}}}); err != nil {
			t.Fatal(err)
		}
		return b
	}
	alice, carol := signUp("alice@example.com"), signUp("carol@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()

	res, err := alice.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "plugins", Loaders: []string{"paper", "spigot"}, GameVersion: "1.21.1", Query: "perms"})
	if err != nil || len(res.GetProjects()) != 1 || res.GetProjects()[0].GetId() != "Vebnzrzj" || res.GetProjects()[0].GetUrl() != "https://modrinth.com/project/luckperms" {
		t.Fatalf("search: %v, %v", res, err)
	}
	if searched != `[["project_type:plugin"],["categories:paper","categories:spigot"],["versions:1.21.1"]]` {
		t.Errorf("facets: %s", searched)
	}
	if _, err := carol.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "plugins", Loaders: []string{"paper"}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider searching: %v", err)
	}
	for name, req := range map[string]*panelv1.SearchAddonsRequest{
		"a kind":         {OrgId: org, Kind: "shaders", Loaders: []string{"paper"}},
		"a loader":       {OrgId: org, Kind: "plugins", Loaders: []string{"evil\"]]"}},
		"no loaders":     {OrgId: org, Kind: "plugins"},
		"a game version": {OrgId: org, Kind: "plugins", Loaders: []string{"paper"}, GameVersion: "latest\"]"},
	} {
		if _, err := alice.addons.SearchAddons(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a bad %s: %v", name, err)
		}
	}

	vs, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "luckperms", Loaders: []string{"paper"}, GameVersion: "1.21.1"})
	if err != nil || len(vs.GetVersions()) != 1 || vs.GetVersions()[0].GetFile().GetSha512() != h2 {
		t.Fatalf("versions: %v, %v", vs, err)
	}
	if _, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "../search", Loaders: []string{"paper"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a path for a project: %v", err)
	}

	id, err := alice.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{OrgId: org, Hashes: []string{h1, h2}, Loaders: []string{"paper"}, GameVersion: "1.21.1"})
	if err != nil || len(id.GetAddons()) != 1 {
		t.Fatalf("identify: %v, %v", id, err)
	}
	if a := id.GetAddons()[0]; a.GetSha512() != h1 || a.GetProject().GetTitle() != "LuckPerms" || a.GetVersion().GetVersionNumber() != "5.4" || a.GetLatest().GetFile().GetSha512() != h2 {
		t.Errorf("identified: %v", a)
	}
}
