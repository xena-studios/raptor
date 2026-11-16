package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/thunderstore"
)

// Thunderstore (#410): search, versions, what an unpacked package is,
// its dependencies (without the BepInEx pack), and the hash the Panel
// takes of a download.
func TestThunderstoreAddons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	zip := []byte("PK\x03\x04 Jotunn")
	version := func(ns, name, v, created string, deps ...string) string {
		return `{"namespace":"` + ns + `","name":"` + name + `","version_number":"` + v + `","date_created":"` + created + `",
			"icon":"https://ccdn.thunderstore.io/live/repository/icons/` + ns + `-` + name + `.png","dependencies":["` + strings.Join(deps, `","`) + `"]}`
	}
	jotunn2 := version("ValheimModding", "Jotunn", "2.30.2", "2026-09-21T00:00:00Z", "denikson-BepInExPack_Valheim-5.4.2333")
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cyberstorm/listing/valheim/":
			_, _ = io.WriteString(w, `{"count":3,"results":[
				{"namespace":"denikson","name":"BepInExPack_Valheim","download_count":9},
				{"namespace":"ValheimModding","name":"Jotunn","download_count":5,"categories":[{"name":"Libraries","slug":"libraries"}]},
				{"namespace":"Someone","name":"Minimap_Colors","download_count":3,"categories":[{"name":"Client-side","slug":"client-side"}]}]}`)
		case "/api/cyberstorm/package/ValheimModding/Jotunn/versions/":
			_, _ = io.WriteString(w, `[{"version_number":"2.20.0","datetime_created":"2024-05-14T00:00:00Z"},{"version_number":"2.30.2","datetime_created":"2026-09-21T00:00:00Z"}]`)
		case "/api/experimental/package/ValheimModding/Jotunn/":
			_, _ = io.WriteString(w, `{"namespace":"ValheimModding","name":"Jotunn","owner":"ValheimModding","latest":`+jotunn2+`,
				"community_listings":[{"community":"valheim","categories":["Libraries"]}]}`)
		case "/api/experimental/package/ValheimModding/Jotunn/2.20.0/":
			_, _ = io.WriteString(w, version("ValheimModding", "Jotunn", "2.20.0", "2024-05-14T00:00:00Z", "denikson-BepInExPack_Valheim-5.4.2202"))
		case "/api/experimental/package/Azumatt/Sleepover/1.0.0/":
			_, _ = io.WriteString(w, version("Azumatt", "Sleepover", "1.0.0", "2026-01-01T00:00:00Z",
				"denikson-BepInExPack_Valheim-5.4.2202", "ValheimModding-Jotunn-2.20.0", "Gone-Missing-1.0.0", "Azumatt-Lib-1.0.0"))
		case "/api/experimental/package/Azumatt/Lib/":
			_, _ = io.WriteString(w, `{"namespace":"Azumatt","name":"Lib","latest":`+version("Azumatt", "Lib", "1.1.0", "2026-01-01T00:00:00Z", "ValheimModding-Jotunn-2.0.0")+`}`)
		case "/package/download/ValheimModding/Jotunn/2.30.2/":
			http.Redirect(w, r, "/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip", http.StatusFound)
		case "/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip":
			_, _ = w.Write(zip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Thunderstore: &thunderstore.Client{Base: ts.URL, CDN: ts.URL, HTTP: ts.Client(), UserAgent: "test"},
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
	valheim := []string{"valheim"}

	// Search: the BepInEx pack left out (the egg installs it), client-only
	// mods marked.
	found, err := alice.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "thunderstore", Source: "thunderstore", Loaders: valheim})
	if err != nil || len(found.GetProjects()) != 2 {
		t.Fatalf("search: %v, %v", found, err)
	}
	if p := found.GetProjects()[0]; p.GetId() != "ValheimModding-Jotunn" || p.GetSource() != "thunderstore" || p.GetUrl() != "https://thunderstore.io/c/valheim/p/ValheimModding/Jotunn/" {
		t.Errorf("project: %v", p)
	}
	if p := found.GetProjects()[1]; p.GetServerSide() != "unsupported" || p.GetTitle() != "Minimap Colors" {
		t.Errorf("a client-only mod: %v", p)
	}
	for name, req := range map[string]*panelv1.SearchAddonsRequest{
		"another kind":      {OrgId: org, Kind: "mods", Source: "thunderstore", Loaders: []string{"fabric"}},
		"another source":    {OrgId: org, Kind: "thunderstore", Source: "modrinth", Loaders: valheim},
		"another community": {OrgId: org, Kind: "thunderstore", Source: "thunderstore", Loaders: []string{"lethal-company"}},
	} {
		if _, err := alice.addons.SearchAddons(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := carol.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "thunderstore", Source: "thunderstore", Loaders: valheim}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider: %v", err)
	}

	// Versions, newest first, with Thunderstore's download link and no hash.
	vs, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, Source: "thunderstore", ProjectId: "ValheimModding-Jotunn", Loaders: valheim})
	if err != nil || len(vs.GetVersions()) != 2 || vs.GetVersions()[0].GetId() != "ValheimModding-Jotunn-2.30.2" ||
		vs.GetVersions()[0].GetFile().GetSha256() != "" || vs.GetVersions()[0].GetFile().GetFilename() != "ValheimModding-Jotunn-2.30.2.zip" {
		t.Fatalf("versions: %v, %v", vs, err)
	}
	if _, err := alice.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, Source: "thunderstore", ProjectId: "../x", Loaders: valheim}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad package: %v", err)
	}

	// What's installed: named by the version Wings unpacked, with the
	// newest offered.
	h, other := strings.Repeat("a", 128), strings.Repeat("b", 128)
	id, err := alice.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{
		OrgId: org, Hashes: []string{h, other}, Loaders: valheim,
		Names: map[string]string{h: "ValheimModding-Jotunn-2.20.0", other: "not a package"},
	})
	if err != nil || len(id.GetAddons()) != 1 {
		t.Fatalf("identify: %v, %v", id, err)
	}
	if a := id.GetAddons()[0]; a.GetProject().GetId() != "ValheimModding-Jotunn" || a.GetVersion().GetVersionNumber() != "2.20.0" ||
		a.GetLatest().GetId() != "ValheimModding-Jotunn-2.30.2" || len(a.GetVersion().GetDependencies()) != 0 {
		t.Errorf("identified: %v", a)
	}

	// Dependencies: required all the way down, each at its newest, the
	// BepInEx pack and what's installed left out, the missing named.
	deps, err := alice.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, Source: "thunderstore", VersionId: "Azumatt-Sleepover-1.0.0", Loaders: valheim,
	})
	if err != nil || len(deps.GetRequired()) != 2 || len(deps.GetUnavailable()) != 1 {
		t.Fatalf("deps: %v, %v", deps, err)
	}
	if r := deps.GetRequired(); r[0].GetVersion().GetId() != "ValheimModding-Jotunn-2.30.2" || r[1].GetProject().GetId() != "Azumatt-Lib" || r[1].GetNeededBy() != "Azumatt-Sleepover" {
		t.Errorf("required: %v", r)
	}
	deps, err = alice.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, Source: "thunderstore", VersionId: "Azumatt-Sleepover-1.0.0", Loaders: valheim, Installed: []string{"ValheimModding-Jotunn"},
	})
	if err != nil || len(deps.GetRequired()) != 1 || deps.GetRequired()[0].GetProject().GetId() != "Azumatt-Lib" {
		t.Errorf("with Jotunn installed: %v, %v", deps, err)
	}
	if _, err := alice.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{OrgId: org, Source: "thunderstore", VersionId: "x", Loaders: valheim}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad version: %v", err)
	}

	// The hash: the Panel downloads the version and gives the CDN's URL.
	hashed, err := alice.addons.HashAddonFile(ctx, &panelv1.HashAddonFileRequest{OrgId: org, Source: "thunderstore", VersionId: "ValheimModding-Jotunn-2.30.2"})
	sum := sha256.Sum256(zip)
	if err != nil || hashed.GetFile().GetSha256() != hex.EncodeToString(sum[:]) || !strings.HasSuffix(hashed.GetFile().GetUrl(), "/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip") {
		t.Fatalf("hash: %v, %v", hashed, err)
	}
	for name, req := range map[string]*panelv1.HashAddonFileRequest{
		"another source": {OrgId: org, Source: "modrinth", VersionId: "ValheimModding-Jotunn-2.30.2"},
		"not a version":  {OrgId: org, Source: "thunderstore", VersionId: "ValheimModding-Jotunn"},
	} {
		if _, err := alice.addons.HashAddonFile(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := alice.addons.HashAddonFile(ctx, &panelv1.HashAddonFileRequest{OrgId: org, Source: "thunderstore", VersionId: "No-Such-1.0.0"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a missing version: %v", err)
	}
	if _, err := carol.addons.HashAddonFile(ctx, &panelv1.HashAddonFileRequest{OrgId: org, Source: "thunderstore", VersionId: "ValheimModding-Jotunn-2.30.2"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider hashing: %v", err)
	}
}
