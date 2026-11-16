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
	"github.com/xena-studios/raptor/internal/panel/curseforge"
	"github.com/xena-studios/raptor/internal/panel/hangar"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/reforger"
	"github.com/xena-studios/raptor/internal/panel/steam"
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
	st := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			http.Error(w, "post a form", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ISteamRemoteStorage/GetPublishedFileDetails/v1/":
			_, _ = io.WriteString(w, `{"response":{"result":1,"publishedfiledetails":[
				{"publishedfileid":"104691717","result":1,"consumer_app_id":4000,"title":"PAC3","file_size":"8707689","time_updated":1760000000,
				 "preview_url":"https://images.steamusercontent.com/ugc/1/A/"},
				{"publishedfileid":"157384458","result":1,"consumer_app_id":4000,"title":"Zombie Survival","file_size":"0","preview_url":"https://evil.example.net/x.png"},
				{"publishedfileid":"5","result":9}]}}`)
		case "/ISteamRemoteStorage/GetCollectionDetails/v1/":
			if r.PostForm.Get("collectioncount") != "3" || r.PostForm.Get("publishedfileids[1]") != "157384458" {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"response":{"result":1,"collectiondetails":[
				{"publishedfileid":"104691717","result":9},
				{"publishedfileid":"157384458","result":1,"children":[{"publishedfileid":"105462463"},{"publishedfileid":"110984714"}]},
				{"publishedfileid":"5","result":9}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer st.Close()

	rf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := func(props string) {
			_, _ = io.WriteString(w, `<html><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":`+props+`}}</script></html>`)
		}
		const mod = `{"id":"595F2BF2F44836FB","name":"RHS - Status Quo","currentVersionNumber":"0.17.5281","author":{"username":"Red Hammer Studios"},
			"previews":[{"url":"https://ar-gcp-cdn.bistudio.com/image/a.jpg"}],"dependencies":[{"asset":{"id":"1337C0DE5DABBEEF","name":"RHS - Content Pack 01"}}]}`
		switch {
		case r.URL.Path == "/workshop/595F2BF2F44836FB":
			page(`{"asset":` + mod + `}`)
		case r.URL.Path == "/workshop" && r.URL.Query().Get("search") == "rhs":
			page(`{"assets":{"count":1,"rows":[` + mod + `]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer rf.Close()

	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "cf-key" {
			http.Error(w, "no key", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		const mod = `{"id":238222,"classId":6,"name":"JEI","slug":"jei","authors":[{"name":"mezz"}]}`
		const blocked = `{"id":5,"classId":6,"name":"Shy","slug":"shy","allowModDistribution":false}`
		file := func(id, mod, date string) string {
			return `{"id":` + id + `,"modId":` + mod + `,"displayName":"v` + id + `","fileName":"f` + id + `.jar","fileDate":"` + date + `","isAvailable":true,
				"downloadUrl":"https://edge.forgecdn.net/files/` + id + `.jar","hashes":[{"value":"` + strings.Repeat("a", 40) + `","algo":1}],
				"gameVersions":["1.21.1","Fabric"],"dependencies":[{"modId":306612,"relationType":3}],"fileFingerprint":` + id + `}`
		}
		switch r.URL.Path {
		case "/v1/mods/search":
			_, _ = io.WriteString(w, `{"data":[`+mod+`],"pagination":{"totalCount":1}}`)
		case "/v1/mods/238222":
			_, _ = io.WriteString(w, `{"data":`+mod+`}`)
		case "/v1/mods/5":
			_, _ = io.WriteString(w, `{"data":`+blocked+`}`)
		case "/v1/mods/238222/files":
			_, _ = io.WriteString(w, `{"data":[`+file("2", "238222", "2026-02-01T00:00:00Z")+`,`+file("1", "238222", "2026-01-01T00:00:00Z")+`]}`)
		case "/v1/mods/5/files":
			_, _ = io.WriteString(w, `{"data":[`+file("9", "5", "2026-01-01T00:00:00Z")+`]}`)
		case "/v1/fingerprints/432":
			_, _ = io.WriteString(w, `{"data":{"exactMatches":[{"file":`+file("1", "238222", "2026-01-01T00:00:00Z")+`}]}}`)
		case "/v1/mods":
			_, _ = io.WriteString(w, `{"data":[`+mod+`]}`)
		case "/v1/mods/238222/files/2":
			_, _ = io.WriteString(w, `{"data":`+file("2", "238222", "2026-02-01T00:00:00Z")+`}`)
		case "/v1/mods/306612":
			_, _ = io.WriteString(w, `{"data":{"id":306612,"classId":6,"name":"Fabric API","slug":"fabric-api"}}`)
		case "/v1/mods/306612/files":
			_, _ = io.WriteString(w, `{"data":[{"id":7,"modId":306612,"displayName":"api","fileName":"api.jar","fileDate":"2026-01-01T00:00:00Z","isAvailable":true,
				"downloadUrl":"https://edge.forgecdn.net/files/7.jar","hashes":[{"value":"`+strings.Repeat("b", 40)+`","algo":1}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cf.Close()

	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Modrinth:   &modrinth.Client{Base: mr.URL, UserAgent: "test"},
		Hangar:     &hangar.Client{Base: hg.URL, UserAgent: "test"},
		Steam:      &steam.Client{Base: st.URL, UserAgent: "test"},
		Reforger:   &reforger.Client{Base: rf.URL, UserAgent: "test"},
		CurseForge: &curseforge.Client{Key: "cf-key", Base: cf.URL, UserAgent: "test"},
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

	// Steam Workshop: items and collections named, previews only from
	// Steam's image host, and what Steam doesn't have left out.
	ws, err := alice.addons.LookupWorkshopItems(ctx, &panelv1.LookupWorkshopItemsRequest{OrgId: org, Ids: []string{"104691717", "157384458", "5"}})
	if err != nil || len(ws.GetItems()) != 2 {
		t.Fatalf("workshop: %v, %v", ws, err)
	}
	if it := ws.GetItems()[0]; it.GetTitle() != "PAC3" || it.GetAppId() != 4000 || it.GetCollection() || it.GetSize() != 8707689 || it.GetPreviewUrl() == "" {
		t.Errorf("item: %v", it)
	}
	if c := ws.GetItems()[1]; !c.GetCollection() || len(c.GetChildren()) != 2 || c.GetPreviewUrl() != "" {
		t.Errorf("collection: %v", c)
	}
	if _, err := alice.addons.LookupWorkshopItems(ctx, &panelv1.LookupWorkshopItemsRequest{OrgId: org, Ids: []string{"12&itemcount=900"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad workshop ID: %v", err)
	}
	if _, err := carol.addons.LookupWorkshopItems(ctx, &panelv1.LookupWorkshopItemsRequest{OrgId: org, Ids: []string{"5"}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider looking up workshop items: %v", err)
	}

	// Arma Reforger Workshop (#417): mods named from their pages, ones it
	// doesn't have left out, and only real IDs asked for.
	rm, err := alice.addons.LookupReforgerMods(ctx, &panelv1.LookupReforgerModsRequest{OrgId: org, Ids: []string{"595F2BF2F44836FB", "0123456789ABCDEF"}})
	if err != nil || len(rm.GetMods()) != 1 {
		t.Fatalf("reforger: %v, %v", rm, err)
	}
	if m := rm.GetMods()[0]; m.GetName() != "RHS - Status Quo" || m.GetVersion() != "0.17.5281" || m.GetAuthor() != "Red Hammer Studios" ||
		m.GetPreviewUrl() == "" || len(m.GetDependencies()) != 1 || m.GetDependencies()[0].GetId() != "1337C0DE5DABBEEF" {
		t.Errorf("reforger mod: %v", m)
	}
	if _, err := alice.addons.LookupReforgerMods(ctx, &panelv1.LookupReforgerModsRequest{OrgId: org, Ids: []string{"../../etc"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad reforger ID: %v", err)
	}
	if rs, err := alice.addons.SearchReforgerMods(ctx, &panelv1.SearchReforgerModsRequest{OrgId: org, Query: "rhs"}); err != nil || rs.GetTotal() != 1 || len(rs.GetMods()) != 1 {
		t.Errorf("reforger search: %v, %v", rs, err)
	}
	if _, err := carol.addons.SearchReforgerMods(ctx, &panelv1.SearchReforgerModsRequest{OrgId: org, Query: "rhs"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider searching reforger mods: %v", err)
	}

	// CurseForge (#412): on with a key; files whose author blocks other
	// apps come with a page to download them by hand.
	if src, err := alice.addons.ListAddonSources(ctx, &panelv1.ListAddonSourcesRequest{OrgId: org}); err != nil || len(src.GetSources()) != 1 || src.GetSources()[0] != "curseforge" {
		t.Errorf("sources: %v, %v", src, err)
	}
	found, err := alice.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "mods", Loaders: []string{"fabric"}, Source: "curseforge"})
	if err != nil || len(found.GetProjects()) != 1 || found.GetProjects()[0].GetId() != "238222" || found.GetProjects()[0].GetSource() != "curseforge" {
		t.Fatalf("curseforge search: %v, %v", found, err)
	}
	cfv, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "238222", Loaders: []string{"fabric"}, Source: "curseforge"})
	if err != nil || len(cfv.GetVersions()) != 2 || cfv.GetVersions()[0].GetFile().GetSha1() == "" || cfv.GetVersions()[0].GetDependencies()[0].GetType() != "required" {
		t.Fatalf("curseforge versions: %v, %v", cfv, err)
	}
	manual, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "5", Loaders: []string{"fabric"}, Source: "curseforge"})
	if err != nil || len(manual.GetVersions()) != 1 || manual.GetVersions()[0].GetFile().GetUrl() != "" ||
		manual.GetVersions()[0].GetFile().GetManualUrl() != "https://www.curseforge.com/minecraft/mc-mods/shy/files/9" {
		t.Fatalf("a blocked file: %v, %v", manual, err)
	}
	h := strings.Repeat("c", 128)
	cfID, err := alice.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{OrgId: org, Hashes: []string{h}, Loaders: []string{"fabric"}, Curseforge: map[string]uint32{h: 1}})
	if err != nil || len(cfID.GetAddons()) != 1 || cfID.GetAddons()[0].GetVersion().GetId() != "1" || cfID.GetAddons()[0].GetLatest().GetId() != "2" {
		t.Fatalf("curseforge identify: %v, %v", cfID, err)
	}
	deps, err := alice.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{OrgId: org, VersionId: "238222@2", Loaders: []string{"fabric"}, Source: "curseforge"})
	if err != nil || len(deps.GetRequired()) != 1 || deps.GetRequired()[0].GetProject().GetTitle() != "Fabric API" || deps.GetRequired()[0].GetVersion().GetFile().GetSha1() == "" {
		t.Fatalf("curseforge dependencies: %v, %v", deps, err)
	}
	if deps, err := alice.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{OrgId: org, VersionId: "238222@2", Loaders: []string{"fabric"}, Source: "curseforge", Installed: []string{"306612"}}); err != nil || len(deps.GetRequired()) != 0 {
		t.Errorf("an installed dependency: %v, %v", deps, err)
	}
	if _, err := alice.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "datapacks", Loaders: []string{"datapack"}, Source: "curseforge"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("curseforge datapacks: %v", err)
	}
}
