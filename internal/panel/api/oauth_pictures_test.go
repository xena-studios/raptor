package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Signing in with Google, GitHub, or Discord copies the picture there,
// once, only to an account that never had one, and only from the
// provider's own picture hosts (docs/PANEL.md#images).
func TestOAuthPictures(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	p := newIdP(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	// The providers' picture hosts, all answered here: which host was asked
	// for is in the request.
	var mu sync.Mutex
	var asked []string
	pics := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Host+r.URL.Path)
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case strings.HasSuffix(r.URL.Path, "/redirect"):
			http.Redirect(w, r, "https://evil.example/x.png", http.StatusFound)
		default:
			_, _ = w.Write(testPNG(t, 400, 400, byte(len(r.URL.Path))))
		}
	}))
	defer pics.Close()
	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, pics.Listener.Addr().String())
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the test's own server, under the providers' names
	}}

	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, DataKey: key, Pictures: client}
	srv := httptest.NewTLSServer(Handler(Config{Auth: svc, AppOrigin: appOrigin}))
	defer srv.Close()
	google := auth.NewOIDC((&oidc.ProviderConfig{
		IssuerURL: p.URL, AuthURL: p.URL + "/google/authorize", TokenURL: p.URL + "/google/token", JWKSURL: p.URL + "/jwks",
		Algorithms: []string{oidc.RS256},
	}).NewProvider(ctx), "client", "secret", srv.URL+"/oauth/google/callback")
	github := auth.NewGitHub("client", "secret", srv.URL+"/oauth/github/callback")
	github.Config.Endpoint = oauth2.Endpoint{AuthURL: p.URL + "/github/authorize", TokenURL: p.URL + "/github/token"}
	github.API = p.URL + "/github"
	discord := auth.NewDiscord("client", "secret", srv.URL+"/oauth/discord/callback")
	discord.Config.Endpoint = oauth2.Endpoint{AuthURL: p.URL + "/discord/authorize", TokenURL: p.URL + "/discord/token"}
	discord.API = p.URL + "/discord"
	svc.OAuth = map[string]auth.OAuthProvider{"google": google, "github": github, "discord": discord}

	signIn := func(b *browser, provider string, id auth.Identity, link bool) {
		t.Helper()
		res, err := b.auth.BeginOAuth(ctx, &panelv1.BeginOAuthRequest{Provider: provider, Link: link})
		if err != nil {
			t.Fatal(err)
		}
		state, code := p.authorize(t, res.GetUrl(), grant{id: id})
		c := *b.http
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.Get(srv.URL + "/oauth/" + provider + "/callback?" + url.Values{"state": {state}, "code": {code}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		svc.WaitPictures()
	}
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
	avatar := func(email string) (id, source string) {
		t.Helper()
		if err := db.QueryRow(ctx, `SELECT coalesce(avatar, ''), avatar_source FROM users WHERE email = $1`, email).Scan(&id, &source); err != nil {
			t.Fatal(err)
		}
		return id, source
	}

	// A new account from GitHub gets its GitHub picture, at 256 pixels.
	ann := newBrowser(t, srv, appOrigin)
	signIn(ann, "github", auth.Identity{Subject: "1", Email: "ann@example.com", EmailVerified: true, Picture: "https://avatars.githubusercontent.com/u/1?v=4"}, false)
	if id, source := avatar("ann@example.com"); id == "" || source != "github" {
		t.Errorf("ann: %q from %q", id, source)
	}
	if got := seen(); len(got) != 1 || got[0] != "avatars.githubusercontent.com/u/1" {
		t.Errorf("asked %v", got)
	}
	// Removed: signing in again doesn't copy it back.
	if _, err := ann.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{}); err != nil {
		t.Fatal(err)
	}
	signIn(ann, "github", auth.Identity{Subject: "1", Email: "ann@example.com", EmailVerified: true, Picture: "https://avatars.githubusercontent.com/u/1?v=4"}, false)
	if id, source := avatar("ann@example.com"); id != "" || source != "removed" {
		t.Errorf("ann after removing hers: %q from %q", id, source)
	}

	// An uploaded picture is never replaced, by a sign-in or a link.
	bea := newBrowser(t, srv, appOrigin)
	signIn(bea, "discord", auth.Identity{Subject: "2", Email: "bea@example.com", EmailVerified: true}, false)
	if id, _ := avatar("bea@example.com"); id != "" {
		t.Errorf("Discord's default picture was copied: %q", id)
	}
	up, err := bea.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{Image: testPNG(t, 64, 64, 9)})
	if err != nil {
		t.Fatal(err)
	}
	signIn(bea, "github", auth.Identity{Subject: "3", Email: "bea@example.com", EmailVerified: true, Picture: "https://avatars.githubusercontent.com/u/3"}, true)
	if id, source := avatar("bea@example.com"); id != up.GetUser().GetAvatar() || source != "upload" {
		t.Errorf("bea's upload was replaced: %q from %q", id, source)
	}

	// Discord's picture, from its hash.
	cal := newBrowser(t, srv, appOrigin)
	signIn(cal, "discord", auth.Identity{Subject: "4", Email: "cal@example.com", EmailVerified: true, Picture: "a_0123456789abcdef0123456789abcdef"}, false)
	if id, source := avatar("cal@example.com"); id == "" || source != "discord" {
		t.Errorf("cal: %q from %q", id, source)
	}
	if got := seen(); got[len(got)-1] != "cdn.discordapp.com/avatars/4/a_0123456789abcdef0123456789abcdef.png" {
		t.Errorf("Discord picture asked as %s", got[len(got)-1])
	}

	// Never from anywhere else, never through a redirect elsewhere, and
	// never what isn't a picture.
	before := len(seen())
	for i, pic := range []string{
		"https://evil.example/picture.png",
		"http://lh3.googleusercontent.com/a/plain-http",
		"https://user:pass@lh3.googleusercontent.com/a/x",
		"https://lh3.googleusercontent.com/a/redirect",
		"https://lh3.googleusercontent.com/a/svg",
	} {
		email := "g" + string(rune('a'+i)) + "@example.com"
		signIn(newBrowser(t, srv, appOrigin), "google", auth.Identity{Subject: "g" + email, Email: email, EmailVerified: true, Picture: pic}, false)
		if id, source := avatar(email); id != "" || source != "" {
			t.Errorf("%s: copied %q", pic, id)
		}
	}
	if got := seen()[before:]; len(got) != 2 || !strings.HasSuffix(got[0], "/redirect") || !strings.HasSuffix(got[1], "/svg") {
		t.Errorf("fetched %v (only the redirect and the SVG from Google's host should be)", got)
	}
	// Google's, at 256 pixels.
	signIn(newBrowser(t, srv, appOrigin), "google", auth.Identity{Subject: "g9", Email: "dee@example.com", EmailVerified: true, Picture: "https://lh3.googleusercontent.com/a/abc=s96-c"}, false)
	if id, source := avatar("dee@example.com"); id == "" || source != "google" {
		t.Errorf("dee: %q from %q", id, source)
	}
}
