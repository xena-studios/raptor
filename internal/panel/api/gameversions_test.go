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
	"github.com/xena-studios/raptor/internal/panel/gameversions"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The version switcher's list comes from the software's own API, through
// the Panel, to members of the org.
func TestListGameVersions(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	apis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/projects/paper" {
			_, _ = io.WriteString(w, `{"versions":{"26.3":["26.3","26.3-rc-1"]}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer apis.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Versions: &gameversions.Client{Bases: &gameversions.Bases{PaperMC: apis.URL}, UserAgent: "test"},
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
	res, err := b.addons.ListGameVersions(ctx, &panelv1.ListGameVersionsRequest{OrgId: org, Source: "paper"})
	if err != nil || len(res.GetVersions()) != 2 || !res.GetVersions()[0].GetStable() || res.GetVersions()[1].GetStable() {
		t.Fatalf("paper: %v, %v", res, err)
	}
	if _, err := b.addons.ListGameVersions(ctx, &panelv1.ListGameVersionsRequest{OrgId: org, Source: "https://evil.example"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown source: %v", err)
	}
	other := newBrowser(t, srv, appOrigin)
	if _, err := other.addons.ListGameVersions(ctx, &panelv1.ListGameVersionsRequest{OrgId: org, Source: "paper"}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("signed out: %v", err)
	}
}
