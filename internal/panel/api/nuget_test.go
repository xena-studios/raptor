package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nuget"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// OpenMod's plugins: search NuGet for the openmod-plugin tag, versions
// with the SHA-512 from NuGet's catalog (packages with no single plugin
// assembly left out), installed files named by ID@version.dll, and
// dependencies with OpenMod's own packages left out.
func TestNuGetAddons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	hash := func(b byte) string { return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(b), 64))) }
	var base string
	// leaf is a registration entry (its files are in catalog).
	leaf := func(id, version string, deps ...string) string {
		var ds []string
		for _, d := range deps {
			ds = append(ds, `{"id":"`+d+`","range":"[1.0.0, )"}`)
		}
		return fmt.Sprintf(`{"catalogEntry":{"@id":"%[1]s/v3/catalog0/data/%[2]s.%[3]s.json","id":"%[2]s","version":"%[3]s","listed":true,
			"published":"2026-01-1%[4]sT00:00:00Z","packageContent":"%[1]s/v3-flatcontainer/%[5]s/%[3]s/%[5]s.%[3]s.nupkg","tags":["openmod","openmod-plugin"],
			"dependencyGroups":[{"dependencies":[%[6]s]}]}}`, base, id, version, version[len(version)-1:], strings.ToLower(id), strings.Join(ds, ","))
	}
	catalog := map[string]string{
		"/v3/catalog0/data/DiFFoZ.Kits.1.0.1.json": `{"packageHash":"` + hash('a') + `","packageHashAlgorithm":"SHA512","packageEntries":[{"fullName":"lib/netstandard2.1/Kits.dll","length":9}]}`,
		"/v3/catalog0/data/DiFFoZ.Kits.1.0.2.json": `{"packageHash":"` + hash('b') + `","packageHashAlgorithm":"SHA512","packageEntries":[{"fullName":"lib/netstandard2.1/Kits.dll","length":9}]}`,
		// Two assemblies: not installable.
		"/v3/catalog0/data/DiFFoZ.Kits.1.0.3.json":             `{"packageHash":"` + hash('c') + `","packageHashAlgorithm":"SHA512","packageEntries":[{"fullName":"lib/net461/A.dll"},{"fullName":"lib/net461/B.dll"}]}`,
		"/v3/catalog0/data/Pustalorc.PlayerInfoLib.3.6.7.json": `{"packageHash":"` + hash('d') + `","packageHashAlgorithm":"SHA512","packageEntries":[{"fullName":"lib/net461/PlayerInfoLib.dll"}]}`,
		"/v3/catalog0/data/Big.Library.1.0.0.json":             `{"packageHash":"` + hash('e') + `","packageHashAlgorithm":"SHA512","packageEntries":[{"fullName":"lib/net461/A.dll"},{"fullName":"lib/net461/B.dll"}]}`,
	}
	srv0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/query":
			if !strings.HasPrefix(r.URL.Query().Get("q"), "tags:openmod-plugin") {
				t.Errorf("search without the tag: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"totalHits":2,"data":[
				{"id":"DiFFoZ.Kits","version":"1.0.2","title":"","description":"Kits","authors":["DiFFoZ"],"totalDownloads":900,"tags":["openmod","openmod-plugin"],"iconUrl":"https://example.com/i.png"},
				{"id":"Untagged","version":"1.0.0","tags":["openmod"]}]}`)
		case p == "/v3/registration5-gz-semver2/diffoz.kits/index.json":
			_, _ = io.WriteString(w, `{"items":[{"@id":"x","items":[`+leaf("DiFFoZ.Kits", "1.0.1")+`,`+
				leaf("DiFFoZ.Kits", "1.0.2", "OpenMod.Unturned", "Pustalorc.PlayerInfoLib", "Big.Library", "System.Text.Json")+`,`+
				leaf("DiFFoZ.Kits", "1.0.3")+`]}]}`)
		case p == "/v3/registration5-gz-semver2/pustalorc.playerinfolib/index.json":
			// A big package: its page is linked, not inlined.
			_, _ = io.WriteString(w, `{"items":[{"@id":"`+base+`/v3/registration5-gz-semver2/pustalorc.playerinfolib/page.json"}]}`)
		case p == "/v3/registration5-gz-semver2/pustalorc.playerinfolib/page.json":
			_, _ = io.WriteString(w, `{"items":[`+leaf("Pustalorc.PlayerInfoLib", "3.6.7", "OpenMod.Core")+`]}`)
		case p == "/v3/registration5-gz-semver2/big.library/index.json":
			_, _ = io.WriteString(w, `{"items":[{"@id":"x","items":[`+leaf("Big.Library", "1.0.0")+`]}]}`)
		case catalog[p] != "":
			_, _ = io.WriteString(w, catalog[p])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv0.Close()
	base = srv0.URL
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		NuGet: &nuget.Client{Search: base, API: base, UserAgent: "test"},
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
	loaders := []string{"openmod"}

	res, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "openmod", Loaders: loaders, Source: "nuget", Query: "kits"})
	if err != nil || len(res.GetProjects()) != 1 {
		t.Fatalf("search: %v, %v", res, err)
	}
	if p := res.GetProjects()[0]; p.GetId() != "diffoz.kits" || p.GetTitle() != "DiFFoZ.Kits" || p.GetIconUrl() != "" || p.GetUrl() != "https://www.nuget.org/packages/DiFFoZ.Kits" {
		t.Errorf("project: %v", p)
	}
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "openmod", Loaders: loaders}); err == nil {
		t.Error("OpenMod's plugins from Modrinth")
	}

	vs, err := b.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "DiFFoZ.Kits", Loaders: loaders, Source: "nuget"})
	if err != nil || len(vs.GetVersions()) != 2 {
		t.Fatalf("versions: %v, %v", vs, err)
	}
	newest := vs.GetVersions()[0]
	f := newest.GetFile()
	if newest.GetId() != "diffoz.kits@1.0.2" || f.GetFilename() != "DiFFoZ.Kits@1.0.2.dll" || f.GetSha512() != strings.Repeat("62", 64) ||
		f.GetUrl() != base+"/v3-flatcontainer/diffoz.kits/1.0.2/diffoz.kits.1.0.2.nupkg" || len(newest.GetDependencies()) != 2 {
		t.Errorf("newest: %v", newest)
	}

	h1, h2 := strings.Repeat("a", 128), strings.Repeat("b", 128)
	id, err := b.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{
		OrgId: org, Loaders: loaders, Hashes: []string{h1, h2},
		Names: map[string]string{h1: "DiFFoZ.Kits@1.0.1.dll", h2: "Kits.dll"},
	})
	if err != nil || len(id.GetAddons()) != 1 {
		t.Fatalf("identify: %v, %v", id, err)
	}
	if a := id.GetAddons()[0]; a.GetVersion().GetVersionNumber() != "1.0.1" || a.GetLatest().GetVersionNumber() != "1.0.2" || a.GetProject().GetId() != "diffoz.kits" {
		t.Errorf("an older Kits: %v", a)
	}

	deps, err := b.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, Source: "nuget", VersionId: "diffoz.kits@1.0.2", Loaders: loaders,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deps.GetRequired()) != 1 || deps.GetRequired()[0].GetVersion().GetFile().GetFilename() != "Pustalorc.PlayerInfoLib@3.6.7.dll" {
		t.Errorf("required: %v", deps.GetRequired())
	}
	if len(deps.GetUnavailable()) != 1 || deps.GetUnavailable()[0].GetSlug() != "Big.Library" {
		t.Errorf("unavailable: %v", deps.GetUnavailable())
	}
	if deps, err := b.addons.ResolveAddonDependencies(ctx, &panelv1.ResolveAddonDependenciesRequest{
		OrgId: org, Source: "nuget", VersionId: "diffoz.kits@1.0.2", Loaders: loaders, Installed: []string{"Pustalorc.PlayerInfoLib", "big.library"},
	}); err != nil || len(deps.GetRequired())+len(deps.GetUnavailable()) != 0 {
		t.Errorf("with both installed: %v, %v", deps, err)
	}
}
