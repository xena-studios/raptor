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
	"github.com/xena-studios/raptor/internal/panel/hangar"
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
	s1, s2 := strings.Repeat("c", 64), strings.Repeat("d", 64)
	var hangarSearch string
	hg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		version := func(id, name, sum string) string {
			return `{"id":` + id + `,"name":"` + name + `","createdAt":"2026-08-01T00:00:00Z","platformDependencies":{"PAPER":["1.21.1"]},
				"downloads":{"PAPER":{"fileInfo":{"name":"Essentials-` + name + `.jar","sizeBytes":10,"sha256Hash":"` + sum + `"},"externalUrl":null,
				"downloadUrl":"https://hangarcdn.papermc.io/plugins/EssentialsX/Essentials/versions/` + name + `/PAPER/Essentials.jar"}}}`
		}
		external := `{"id":9,"name":"ext","downloads":{"PAPER":{"fileInfo":null,"externalUrl":"https://evil.example.net/x.jar","downloadUrl":null}}}`
		switch r.URL.Path {
		case "/projects":
			hangarSearch = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"pagination":{"count":1},"result":[{"id":23,"name":"Essentials","namespace":{"owner":"EssentialsX","slug":"Essentials"},
				"description":"The essentials","avatarUrl":"https://hangarcdn.papermc.io/avatars/project/23.webp","stats":{"downloads":7}}]}`)
		case "/projects/Essentials/versions":
			_, _ = io.WriteString(w, `{"result":[`+version("3", "2.21", s1)+`,`+external+`,`+version("2", "2.20", s2)+`]}`)
		case "/versions/hash/" + s2:
			_, _ = io.WriteString(w, `{"id":23,"name":"Essentials","namespace":{"owner":"EssentialsX","slug":"Essentials"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hg.Close()

	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Modrinth: &modrinth.Client{Base: mr.URL, UserAgent: "test"},
		Hangar:   &hangar.Client{Base: hg.URL, UserAgent: "test"},
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

	// Hangar: plugins for Paper, Velocity, and Waterfall, files with a
	// SHA-256 on Hangar's CDN only.
	hs, err := alice.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Source: "hangar", Kind: "plugins", Loaders: []string{"paper"}, GameVersion: "1.21.1"})
	if err != nil || len(hs.GetProjects()) != 1 || hs.GetProjects()[0].GetId() != "Essentials" || hs.GetProjects()[0].GetSource() != "hangar" ||
		hs.GetProjects()[0].GetUrl() != "https://hangar.papermc.io/EssentialsX/Essentials" {
		t.Fatalf("hangar search: %v, %v", hs, err)
	}
	if !strings.Contains(hangarSearch, "platform=PAPER") || !strings.Contains(hangarSearch, "version=1.21.1") || !strings.Contains(hangarSearch, "sort=-downloads") {
		t.Errorf("hangar query: %s", hangarSearch)
	}
	for name, req := range map[string]*panelv1.SearchAddonsRequest{
		"source":            {OrgId: org, Source: "spigotmc", Kind: "plugins", Loaders: []string{"paper"}},
		"kind for hangar":   {OrgId: org, Source: "hangar", Kind: "mods", Loaders: []string{"fabric"}},
		"loader for hangar": {OrgId: org, Source: "hangar", Kind: "plugins", Loaders: []string{"sponge"}},
	} {
		if _, err := alice.addons.SearchAddons(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a bad %s: %v", name, err)
		}
	}
	hv, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, Source: "hangar", ProjectId: "Essentials", Loaders: []string{"paper"}, GameVersion: "1.21.1"})
	if err != nil || len(hv.GetVersions()) != 2 || hv.GetVersions()[0].GetFile().GetSha256() != s1 || hv.GetVersions()[0].GetFile().GetSha512() != "" {
		t.Fatalf("hangar versions (the external one left out): %v, %v", hv, err)
	}
	id, err = alice.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{
		OrgId: org, Hashes: []string{h1, h2}, Sha256: map[string]string{h1: s1, h2: s2}, Loaders: []string{"paper"}, GameVersion: "1.21.1",
	})
	if err != nil || len(id.GetAddons()) != 2 {
		t.Fatalf("identify on both: %v, %v", id, err)
	}
	if a := id.GetAddons()[1]; a.GetSha512() != h2 || a.GetProject().GetSource() != "hangar" || a.GetVersion().GetVersionNumber() != "2.20" || a.GetLatest().GetFile().GetSha256() != s1 {
		t.Errorf("identified on hangar: %v", a)
	}
	if _, err := alice.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{OrgId: org, Hashes: []string{h1}, Sha256: map[string]string{h1: "nope"}, Loaders: []string{"paper"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad SHA-256: %v", err)
	}
}
