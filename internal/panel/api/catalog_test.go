package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The catalog: signed-in users only; listed without the egg files, and each
// file as servers get it, with the catalog's x-raptor block.
func TestCatalog(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, AppOrigin: appOrigin}))
	defer srv.Close()

	b := newBrowser(t, srv, appOrigin)
	if _, err := b.eggs.ListEggs(ctx, &panelv1.ListEggsRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("signed out: %v", err)
	}
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}

	list, err := b.eggs.ListEggs(ctx, &panelv1.ListEggsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var paper *panelv1.CatalogEgg
	for _, e := range list.GetEggs() {
		if e.GetId() == "minecraft/paper" {
			paper = e
		}
	}
	if paper == nil || paper.GetCategory() != "minecraft" || !paper.GetCertified() || len(paper.GetImages()) == 0 ||
		len(paper.GetVariables()) == 0 || paper.GetSourceUrl() == "" {
		t.Fatalf("paper: %v", paper)
	}
	got, err := b.eggs.GetEgg(ctx, &panelv1.GetEggRequest{Id: "minecraft/paper"})
	if err != nil {
		t.Fatal(err)
	}
	egg, err := eggs.Parse(got.GetEgg())
	if err != nil || egg.Raptor.Players.Query != "minecraft" || !egg.SupportsArch("arm64") {
		t.Errorf("paper's egg: %+v, %v", egg, err)
	}
	if _, err := b.eggs.GetEgg(ctx, &panelv1.GetEggRequest{Id: "../../etc/passwd"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown egg: %v", err)
	}
}
