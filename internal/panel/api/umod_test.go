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
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/umod"
)

// Rust's plugins come from uMod: search, the newest file with its SHA-1,
// and installed files named by their file names.
func TestUModAddons(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	const sum = "735e62b66430192cb96a9465fa7acdb9c57a9ca1"
	kits := `{"name":"Kits","title":"Rust Kits","slug":"rust-kits","author":"k1lly0u","downloads":5,"icon_url":"https://assets.umod.org/i.png",
		"download_url":"https://umod.org/plugins/Kits.cs","latest_release_version":"4.4.10","latest_release_version_checksum":"` + sum + `"}`
	vanish := `{"name":"Vanish","title":"Vanish","slug":"vanish","icon_url":"https://evil.example/i.png",
		"download_url":"https://umod.org/plugins/Vanish.cs","latest_release_version":"1.0","latest_release_version_checksum":"` + strings.Repeat("1", 40) + `"}`
	um := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins/search.json":
			switch r.URL.Query().Get("query") {
			case "Kits":
				_, _ = io.WriteString(w, `{"total":1,"data":[`+kits+`]}`)
			case "Vanish":
				_, _ = io.WriteString(w, `{"total":1,"data":[`+vanish+`]}`)
			case "":
				_, _ = io.WriteString(w, `{"total":2,"data":[`+kits+`,`+vanish+`]}`)
			default:
				_, _ = io.WriteString(w, `{"total":0,"data":[]}`)
			}
		case "/plugins/rust-kits.json":
			_, _ = io.WriteString(w, kits)
		default:
			http.NotFound(w, r)
		}
	}))
	defer um.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		UMod: &umod.Client{Base: um.URL, UserAgent: "test"},
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
	loaders := []string{"oxide", "carbon"}

	res, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "umod", Loaders: loaders, Source: "umod"})
	if err != nil || len(res.GetProjects()) != 2 || res.GetProjects()[0].GetUrl() != "https://umod.org/plugins/rust-kits" || res.GetProjects()[0].GetIconUrl() == "" || res.GetProjects()[1].GetIconUrl() != "" {
		t.Fatalf("search: %v, %v", res, err)
	}
	if _, err := b.addons.SearchAddons(ctx, &panelv1.SearchAddonsRequest{OrgId: org, Kind: "umod", Loaders: loaders}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("umod plugins from Modrinth: %v", err)
	}
	vs, err := b.addons.ListAddonVersions(ctx, &panelv1.ListAddonVersionsRequest{OrgId: org, ProjectId: "rust-kits", Loaders: loaders, Source: "umod"})
	if err != nil || len(vs.GetVersions()) != 1 || vs.GetVersions()[0].GetFile().GetSha1() != sum || vs.GetVersions()[0].GetFile().GetFilename() != "Kits.cs" {
		t.Fatalf("versions: %v, %v", vs, err)
	}

	h1, h2, h3 := strings.Repeat("a", 128), strings.Repeat("b", 128), strings.Repeat("c", 128)
	id, err := b.addons.IdentifyAddons(ctx, &panelv1.IdentifyAddonsRequest{
		OrgId: org, Loaders: loaders, Hashes: []string{h1, h2, h3},
		Names: map[string]string{h1: "Kits.cs", h2: "Vanish.cs", h3: "Mine.cs"},
		Sha1:  map[string]string{h1: sum, h2: strings.Repeat("2", 40), h3: strings.Repeat("3", 40)},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*panelv1.IdentifiedAddon{}
	for _, a := range id.GetAddons() {
		got[a.GetSha512()] = a
	}
	if a := got[h1]; a == nil || a.GetVersion().GetVersionNumber() != "4.4.10" || a.GetLatest() != nil {
		t.Errorf("the newest Kits: %v", a)
	}
	if a := got[h2]; a == nil || a.GetVersion() != nil || a.GetLatest().GetVersionNumber() != "1.0" {
		t.Errorf("an older Vanish: %v", a)
	}
	if got[h3] != nil {
		t.Errorf("a plugin uMod doesn't have: %v", got[h3])
	}
}
