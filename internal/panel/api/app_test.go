package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

const (
	appClient   = "raptor-mobile"
	appRedirect = "net.raptorpanel.app:/oauth"
)

// phone is the app: no cookies, no Origin, its access token as a bearer.
type phone struct {
	token string
	auth  panelv1connect.AuthServiceClient
	staff panelv1connect.StaffServiceClient
}

func newPhone(srv *httptest.Server) *phone {
	p := &phone{}
	bearer := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if p.token != "" {
				req.Header().Set("Authorization", "Bearer "+p.token)
			}
			return next(ctx, req)
		}
	})
	p.auth = panelv1connect.NewAuthServiceClient(srv.Client(), srv.URL+"/api", connect.WithInterceptors(bearer))
	p.staff = panelv1connect.NewStaffServiceClient(srv.Client(), srv.URL+"/api", connect.WithInterceptors(bearer))
	return p
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestPhoneApp(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	var now time.Time
	clock := func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Staff: &staff.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
	defer srv.Close()

	token := func(form url.Values, header ...string) (tokens, int) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+auth.AppTokenPath, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Error("token responses must not be cached")
		}
		var out tokens
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out, resp.StatusCode
	}
	exchange := func(code, verifier string) (tokens, int) {
		t.Helper()
		return token(url.Values{"grant_type": {"authorization_code"}, "client_id": {appClient}, "redirect_uri": {appRedirect}, "code": {code}, "code_verifier": {verifier}})
	}
	refresh := func(rt string) (tokens, int) {
		t.Helper()
		return token(url.Values{"grant_type": {"refresh_token"}, "client_id": {appClient}, "refresh_token": {rt}})
	}

	// Alice signs in to the web app by email; with no passkey, that counts
	// as having just confirmed it's her.
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

	verifier := "a-verifier-that-is-long-enough-for-pkce-0123456789"
	approve := func(req *panelv1.ApproveAppRequest) (string, error) {
		res, err := b.auth.ApproveApp(ctx, req)
		return res.GetCode(), err
	}
	good := &panelv1.ApproveAppRequest{ClientId: appClient, RedirectUri: appRedirect, CodeChallenge: pkce(verifier), DeviceName: "Alice's iPhone\x00\n"}

	// Only Raptor's app, sent back only to its own address, with PKCE.
	for name, req := range map[string]*panelv1.ApproveAppRequest{
		"another client":    {ClientId: "evil", RedirectUri: appRedirect, CodeChallenge: pkce(verifier)},
		"another address":   {ClientId: appClient, RedirectUri: "https://evil.example/cb", CodeChallenge: pkce(verifier)},
		"no challenge":      {ClientId: appClient, RedirectUri: appRedirect},
		"a plain challenge": {ClientId: appClient, RedirectUri: appRedirect, CodeChallenge: verifier},
	} {
		if _, err := approve(req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	// It needs a fresh confirmation.
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	if _, err := approve(good); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("approving without a recent confirmation: %v", err)
	}
	now = time.Time{}

	// A wrong verifier spoils the code for good.
	c, err := approve(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, status := exchange(c, "not-the-verifier"); status != http.StatusBadRequest {
		t.Errorf("wrong verifier: %d", status)
	}
	if _, status := exchange(c, verifier); status != http.StatusBadRequest {
		t.Errorf("a code after a failed try: %d", status)
	}
	// Codes expire.
	c, _ = approve(good)
	now = time.Now().Add(auth.AppCodeTTL + time.Minute)
	if _, status := exchange(c, verifier); status != http.StatusBadRequest {
		t.Errorf("expired code: %d", status)
	}
	now = time.Time{}
	// A browser can't use the token endpoint.
	c, _ = approve(good)
	if _, status := token(url.Values{"grant_type": {"authorization_code"}, "client_id": {appClient}, "redirect_uri": {appRedirect}, "code": {c}, "code_verifier": {verifier}}, "Origin", appOrigin); status != http.StatusForbidden {
		t.Errorf("token endpoint from a browser: %d", status)
	}
	// Nor can the code go to another address than the one approved.
	if _, status := token(url.Values{"grant_type": {"authorization_code"}, "client_id": {appClient}, "redirect_uri": {"net.raptorpanel.app:/other"}, "code": {c}, "code_verifier": {verifier}}); status != http.StatusBadRequest {
		t.Errorf("another redirect_uri: %d", status)
	}

	// The real thing.
	c, _ = approve(good)
	got, status := exchange(c, verifier)
	if status != http.StatusOK || got.TokenType != "Bearer" || got.AccessToken == "" || got.RefreshToken == "" || got.ExpiresIn != int(auth.AppAccessTTL.Seconds()) {
		t.Fatalf("exchange: %d %+v", status, got)
	}
	if _, status := exchange(c, verifier); status != http.StatusBadRequest {
		t.Errorf("code used twice: %d", status)
	}
	if !strings.Contains(mail.mail[len(mail.mail)-1], "Raptor's app signed in") {
		t.Error("no email about the app signing in")
	}

	p := newPhone(srv)
	if _, err := p.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no token: %v", err)
	}
	p.token = got.AccessToken
	me, err := p.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if err != nil || me.GetUser().GetEmail() != "alice@example.com" || me.GetSession().GetClient() != appClient || me.GetSession().GetDeviceName() != "Alice's iPhone" {
		t.Fatalf("the app's session: %v, %v", me, err)
	}

	// The access token is only a bearer, and the cookie only a cookie.
	u, _ := url.Parse(srv.URL)
	var cookie string
	for _, ck := range b.http.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			cookie = ck.Value
		}
	}
	if cookie == "" {
		t.Fatal("the browser has no session cookie")
	}
	asBearer := newPhone(srv)
	asBearer.token = cookie
	if _, err := asBearer.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a browser's cookie as a bearer token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/raptor.panel.v1.AuthService/GetSession", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: got.AccessToken})
	if resp, err := srv.Client().Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the app's token as a cookie: %v, %v", resp.StatusCode, err)
	} else {
		_ = resp.Body.Close()
	}

	// What the app can't do: confirm it's Alice, so nothing that needs it,
	// approve another app, or see staff tools.
	if _, err := p.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("the app re-authenticating: %v", err)
	}
	if _, err := p.auth.DeleteAccount(ctx, &panelv1.DeleteAccountRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("the app deleting the account: %v", err)
	}
	if _, err := p.auth.ApproveApp(ctx, good); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("the app approving an app: %v", err)
	}
	if me, err := p.staff.GetStaffMe(ctx, &panelv1.GetStaffMeRequest{}); err != nil || me.GetRole() != "" {
		t.Errorf("staff in the app: %v, %v", me, err)
	}

	// The phone is one of Alice's devices.
	list, err := b.auth.ListSessions(ctx, &panelv1.ListSessionsRequest{})
	if err != nil || len(list.GetSessions()) != 2 {
		t.Fatalf("devices: %v, %v", list, err)
	}

	// Access tokens last an hour; the refresh token gets new ones, once.
	now = time.Now().Add(auth.AppAccessTTL + time.Minute)
	if _, err := p.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an expired access token: %v", err)
	}
	next, status := refresh(got.RefreshToken)
	if status != http.StatusOK || next.AccessToken == got.AccessToken || next.RefreshToken == got.RefreshToken {
		t.Fatalf("refresh: %d %+v", status, next)
	}
	p.token = next.AccessToken
	if _, err := p.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); err != nil {
		t.Errorf("after refreshing: %v", err)
	}
	// The old refresh token again means it was copied: the session ends.
	if _, status := refresh(got.RefreshToken); status != http.StatusBadRequest {
		t.Errorf("a used refresh token: %d", status)
	}
	if _, err := p.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("still signed in after a refresh token was reused: %v", err)
	}
	if _, status := refresh(next.RefreshToken); status != http.StatusBadRequest {
		t.Errorf("refreshing a session that was ended: %d", status)
	}
	now = time.Time{}

	// Signing the phone out from the browser ends it.
	c, _ = approve(good)
	got, _ = exchange(c, verifier)
	p.token = got.AccessToken
	me, _ = p.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if _, err := b.auth.RevokeSession(ctx, &panelv1.RevokeSessionRequest{Target: &panelv1.RevokeSessionRequest_Id{Id: me.GetSession().GetId()}}); err != nil {
		t.Fatal(err)
	}
	if _, status := refresh(got.RefreshToken); status != http.StatusBadRequest {
		t.Errorf("refreshing after being signed out: %d", status)
	}

	// No 90-day limit, but 30 days without the app ends it.
	c, _ = approve(good)
	got, _ = exchange(c, verifier)
	now = time.Now().Add(auth.SessionIdle - time.Hour)
	if got, status = refresh(got.RefreshToken); status != http.StatusOK {
		t.Fatalf("refresh after 29 days: %d", status)
	}
	now = now.Add(auth.SessionIdle - time.Hour)
	if got, status = refresh(got.RefreshToken); status != http.StatusOK {
		t.Fatalf("refresh after 58 days: %d", status)
	}
	now = now.Add(auth.SessionIdle + time.Hour)
	if _, status = refresh(got.RefreshToken); status != http.StatusBadRequest {
		t.Errorf("refresh after 30 idle days: %d", status)
	}
	now = time.Time{}

	// Signing out in the app ends its session.
	c, _ = approve(good)
	got, _ = exchange(c, verifier)
	p.token = got.AccessToken
	if _, err := p.auth.SignOut(ctx, &panelv1.SignOutRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, status := refresh(got.RefreshToken); status != http.StatusBadRequest {
		t.Errorf("refreshing after signing out: %d", status)
	}

	// Bad requests.
	if _, status := token(url.Values{"grant_type": {"password"}, "client_id": {appClient}}); status != http.StatusBadRequest {
		t.Errorf("another grant type: %d", status)
	}
	if _, status := token(url.Values{"grant_type": {"refresh_token"}, "client_id": {"evil"}, "refresh_token": {"x"}}); status != http.StatusUnauthorized {
		t.Errorf("another client: %d", status)
	}
}
