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
	"github.com/xena-studios/raptor/internal/panel/mojang"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Players are looked up by name for a stopped server's lists: Mojang's
// UUID, or the offline one without asking; names are checked first.
func TestLookupMinecraftPlayer(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	mj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/profiles/minecraft/notch" {
			_, _ = io.WriteString(w, `{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer mj.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin,
		Mojang: &mojang.Client{Base: mj.URL, UserAgent: "test"},
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
	p, err := b.addons.LookupMinecraftPlayer(ctx, &panelv1.LookupMinecraftPlayerRequest{OrgId: org, Name: "notch"})
	if err != nil || p.GetName() != "Notch" || p.GetUuid() != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Fatalf("online: %v, %v", p, err)
	}
	p, err = b.addons.LookupMinecraftPlayer(ctx, &panelv1.LookupMinecraftPlayerRequest{OrgId: org, Name: "Notch", Offline: true})
	if err != nil || p.GetUuid() != mojang.OfflineUUID("Notch") {
		t.Errorf("offline: %v, %v", p, err)
	}
	if _, err := b.addons.LookupMinecraftPlayer(ctx, &panelv1.LookupMinecraftPlayerRequest{OrgId: org, Name: "nobody"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("nobody: %v", err)
	}
	if _, err := b.addons.LookupMinecraftPlayer(ctx, &panelv1.LookupMinecraftPlayerRequest{OrgId: org, Name: "a; op me"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("not a name: %v", err)
	}
}
