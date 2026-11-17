package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// GetSession brings raptorpanel.net's "signed in" hint in line over HTTP:
// a browser signed in before the hint existed gets it, and one whose
// session ended has its leftover hint cleared, even though that answer is
// an error (DECISIONS #374).
func TestGetSessionSyncsHint(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, HintDomain: "raptorpanel.net"}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
	defer srv.Close()

	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	var session string
	for _, c := range b.http.Jar.Cookies(u) {
		if c.Name == auth.CookieName {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("no session cookie after signing in")
	}

	get := func(cookie string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/raptor.panel.v1.AuthService/GetSession", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", appOrigin)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		for _, v := range resp.Header.Values("Set-Cookie") {
			if strings.HasPrefix(v, auth.HintCookie+"=") {
				return resp.StatusCode, v
			}
		}
		return resp.StatusCode, ""
	}

	if status, set := get(auth.CookieName + "=" + session); status != http.StatusOK || !strings.HasPrefix(set, "raptor_signed_in=1") {
		t.Errorf("signed in without the hint: %d %q, want it set", status, set)
	}
	if _, set := get(auth.CookieName + "=" + session + "; raptor_signed_in=1"); set != "" {
		t.Errorf("signed in with the hint: %q, want nothing written", set)
	}
	if status, set := get("raptor_signed_in=1"); status != http.StatusUnauthorized || !strings.Contains(set, "Max-Age=0") {
		t.Errorf("signed out with a leftover hint: %d %q, want it cleared", status, set)
	}
}
