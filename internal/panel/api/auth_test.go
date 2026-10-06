package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

const appOrigin = "https://app.example.test"

type inbox struct {
	mu   sync.Mutex
	mail []string
}

func (b *inbox) Send(_ context.Context, to, _, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mail = append(b.mail, to+"\n"+text)
	return nil
}

var (
	codeRe = regexp.MustCompile(`code is (\d{6})`)
	linkRe = regexp.MustCompile(`/signin/link#([A-Za-z0-9_-]+)`)
)

// last returns the newest email's code and link token.
func (b *inbox) last(t *testing.T) (code, link string) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.mail) == 0 {
		t.Fatal("no email")
	}
	m := b.mail[len(b.mail)-1]
	return codeRe.FindStringSubmatch(m)[1], linkRe.FindStringSubmatch(m)[1]
}

// browser is a client like the web app: HTTPS, a cookie jar, and the app's
// Origin on every request.
type browser struct {
	http *http.Client
	auth panelv1connect.AuthServiceClient
}

func newBrowser(t *testing.T, srv *httptest.Server, origin string) *browser {
	jar, _ := cookiejar.New(nil)
	c := *srv.Client() // a copy: each browser has its own cookies
	c.Jar = jar
	withOrigin := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Origin", origin)
			return next(ctx, req)
		}
	})
	return &browser{http: &c, auth: panelv1connect.NewAuthServiceClient(&c, srv.URL+"/api", connect.WithInterceptors(withOrigin))}
}

func TestEmailSignIn(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	var now time.Time
	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: svc, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)

	if _, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("signed out: %v", err)
	}
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: " Alice@Example.COM "}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	finish := func(email, code string) (*panelv1.FinishEmailSignInResponse, error) {
		return b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := finish("alice@example.com", wrong); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("wrong code: %v", err)
	}
	res, err := finish("alice@example.com", code)
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetNewAccount() || res.GetUser().GetEmail() != "alice@example.com" {
		t.Errorf("signed up: %v", res)
	}
	me, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if err != nil || me.GetUser().GetId() != res.GetUser().GetId() || !me.GetSession().GetCurrent() {
		t.Fatalf("session: %v, %v", me, err)
	}
	// The cookie as the spec has it.
	resp, _ := b.http.Post(srv.URL+"/api/raptor.panel.v1.AuthService/GetSession", "application/json", strings.NewReader("{}"))
	_ = resp.Body.Close()
	if _, err := finish("alice@example.com", code); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code reused: %v", err)
	}

	// A second device, by link this time: the same account.
	b2 := newBrowser(t, srv, appOrigin)
	if _, err := b2.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	_, link := mail.last(t)
	res2, err := b2.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_LinkToken{LinkToken: link}})
	if err != nil || res2.GetNewAccount() || res2.GetUser().GetId() != res.GetUser().GetId() {
		t.Fatalf("link: %v, %v", res2, err)
	}
	if _, err := b2.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_LinkToken{LinkToken: link}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("link reused: %v", err)
	}
	list, err := b.auth.ListSessions(ctx, &panelv1.ListSessionsRequest{})
	if err != nil || len(list.GetSessions()) != 2 {
		t.Fatalf("sessions: %v, %v", list, err)
	}
	// Sign the other device out from the first.
	if _, err := b.auth.RevokeSession(ctx, &panelv1.RevokeSessionRequest{Target: &panelv1.RevokeSessionRequest_AllOthers{AllOthers: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("revoked device still signed in: %v", err)
	}

	// Five wrong guesses use a code up, right one included.
	if _, err := b2.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ = mail.last(t)
	for range auth.CodeAttempts {
		_, _ = b2.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: "bob@example.com", Code: wrong},
		}})
	}
	if _, err := b2.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "bob@example.com", Code: code},
	}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code after %d wrong guesses: %v", auth.CodeAttempts, err)
	}

	// Codes expire.
	if _, err := b2.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "carol@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ = mail.last(t)
	now = time.Now().Add(auth.CodeTTL + time.Minute)
	if _, err := b2.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "carol@example.com", Code: code},
	}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("expired code: %v", err)
	}
	// Sessions end after 30 idle days.
	now = time.Now().Add(auth.SessionIdle + time.Hour)
	if _, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("idle session: %v", err)
	}
	now = time.Time{}

	// Signing out ends the session.
	if _, err := b.auth.SignOut(ctx, &panelv1.SignOutRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("after sign-out: %v", err)
	}

	// Sending is rate limited per address.
	var last error
	for range 10 {
		_, last = b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "dave@example.com"})
	}
	if connect.CodeOf(last) != connect.CodeResourceExhausted {
		t.Errorf("rate limit: %v", last)
	}
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "not an email"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("bad address: %v", err)
	}
}

// The cookie, and the API refusing other sites.
func TestBrowserGuard(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	srv := httptest.NewTLSServer(Handler(Config{Auth: &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}, AppOrigin: appOrigin}))
	defer srv.Close()

	// A sibling site calling with the user's cookie is refused outright.
	evil := newBrowser(t, srv, "https://www.example.test")
	if _, err := evil.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "a@example.com"}); err == nil {
		t.Error("another origin was served")
	}
	// Preflight from the app: CORS for exactly that origin, with credentials.
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/raptor.panel.v1.AuthService/GetSession", nil)
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != appOrigin || resp.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("preflight: %v", resp.Header)
	}

	// The session cookie: __Host-, Secure, HttpOnly, SameSite=Strict, Path=/,
	// no Domain.
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	var setCookie string
	capture := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Origin", appOrigin)
			res, err := next(ctx, req)
			if err == nil {
				setCookie = res.Header().Get("Set-Cookie")
			}
			return res, err
		}
	})
	client := panelv1connect.NewAuthServiceClient(srv.Client(), srv.URL+"/api", connect.WithInterceptors(capture))
	if _, err := client.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "a@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{auth.CookieName + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("cookie %q lacks %q", setCookie, want)
		}
	}
	if strings.Contains(strings.ToLower(setCookie), "domain=") {
		t.Errorf("cookie has a Domain: %q", setCookie)
	}
}
