package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/pquerna/otp/totp"
	"golang.org/x/oauth2"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// idp is a fake Google, GitHub, and Discord: it checks PKCE, signs real ID
// tokens, and answers the user APIs.
type idp struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	codes  map[string]grant
	tokens map[string]auth.Identity
}

type grant struct {
	id        auth.Identity
	nonce     string
	challenge string
	// Ways to spoil the ID token.
	aud string
}

func newIdP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key, codes: map[string]grant{}, tokens: map[string]auth.Identity{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /{provider}/token", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		g, ok := p.codes[r.FormValue("code")]
		delete(p.codes, r.FormValue("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		access := rand.Text()
		p.mu.Lock()
		p.tokens[access] = g.id
		p.mu.Unlock()
		out := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600}
		if r.PathValue("provider") == "google" {
			out["id_token"] = p.idToken(t, g)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	user := func(r *http.Request) (auth.Identity, bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		id, ok := p.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		return id, ok
	}
	mux.HandleFunc("GET /github/user", func(w http.ResponseWriter, r *http.Request) {
		id, ok := user(r)
		if !ok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": json.Number(id.Subject), "login": "someone"})
	})
	mux.HandleFunc("GET /github/user/emails", func(w http.ResponseWriter, r *http.Request) {
		id, ok := user(r)
		if !ok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "other@example.com", "primary": false, "verified": true},
			{"email": id.Email, "primary": true, "verified": id.EmailVerified},
		})
	})
	mux.HandleFunc("GET /discord/users/@me", func(w http.ResponseWriter, r *http.Request) {
		id, ok := user(r)
		if !ok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id.Subject, "email": id.Email, "verified": id.EmailVerified})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *idp) idToken(t *testing.T, g grant) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "k1"}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Error(err)
		return ""
	}
	aud := g.aud
	if aud == "" {
		aud = "client"
	}
	claims, _ := json.Marshal(map[string]any{
		"iss": p.URL, "sub": g.id.Subject, "aud": aud, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"nonce": g.nonce, "email": g.id.Email, "email_verified": g.id.EmailVerified,
	})
	jws, err := signer.Sign(claims)
	if err != nil {
		t.Error(err)
		return ""
	}
	s, _ := jws.CompactSerialize()
	return s
}

// authorize plays the user approving at the provider: it returns the state
// and code the provider sends the browser back with.
func (p *idp) authorize(t *testing.T, authURL string, g grant) (state, code string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("no PKCE: %s", authURL)
	}
	if g.nonce == "" {
		g.nonce = q.Get("nonce")
	}
	g.challenge = q.Get("code_challenge")
	code = rand.Text()
	p.mu.Lock()
	p.codes[code] = g
	p.mu.Unlock()
	return q.Get("state"), code
}

func TestOAuth(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	p := newIdP(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	var now time.Time
	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, DataKey: key, Now: func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}}
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

	// back follows the provider's redirect to the callback and returns
	// where the API sends the browser next.
	back := func(b *browser, provider, state, code string) string {
		t.Helper()
		c := *b.http
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.Get(srv.URL + "/oauth/" + provider + "/callback?" + url.Values{"state": {state}, "code": {code}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), appOrigin+"/") {
			t.Fatalf("callback: %s %s", resp.Status, resp.Header.Get("Location"))
		}
		return strings.TrimPrefix(resp.Header.Get("Location"), appOrigin)
	}
	begin := func(b *browser, provider string, link bool) (string, error) {
		t.Helper()
		res, err := b.auth.BeginOAuth(ctx, &panelv1.BeginOAuthRequest{Provider: provider, Link: link})
		return res.GetUrl(), err
	}
	signIn := func(b *browser, provider string, g grant) string {
		t.Helper()
		u, err := begin(b, provider, false)
		if err != nil {
			t.Fatal(err)
		}
		state, code := p.authorize(t, u, g)
		return back(b, provider, state, code)
	}
	whoami := func(b *browser) string {
		s, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
		if err != nil {
			return ""
		}
		return s.GetUser().GetEmail()
	}

	m, err := newBrowser(t, srv, appOrigin).auth.GetSignInMethods(ctx, &panelv1.GetSignInMethodsRequest{})
	if err != nil || strings.Join(m.GetOauthProviders(), ",") != "discord,github,google" {
		t.Fatalf("methods: %v, %v", m, err)
	}

	// A new Google account with a verified address makes a Raptor account.
	b := newBrowser(t, srv, appOrigin)
	carol := auth.Identity{Subject: "g-carol", Email: "Carol@Example.com", EmailVerified: true}
	if dest := signIn(b, "google", grant{id: carol}); dest != "/" || whoami(b) != "carol@example.com" {
		t.Fatalf("google sign-up: %s, %q", dest, whoami(b))
	}
	// The ID token must be for this client and this sign-in.
	for what, g := range map[string]grant{
		"another client's ID token": {id: carol, aud: "someone-else"},
		"another sign-in's nonce":   {id: carol, nonce: "stale"},
	} {
		b2 := newBrowser(t, srv, appOrigin)
		if dest := signIn(b2, "google", g); dest != "/signin?error=oauth_failed" || whoami(b2) != "" {
			t.Errorf("%s: %s", what, dest)
		}
	}

	// GitHub, with alice's verified address: joins her existing account,
	// and tells her.
	alice := newBrowser(t, srv, appOrigin)
	if _, err := alice.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := alice.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}
	b = newBrowser(t, srv, appOrigin)
	if dest := signIn(b, "github", grant{id: auth.Identity{Subject: "1001", Email: "alice@example.com", EmailVerified: true}}); dest != "/" || whoami(b) != "alice@example.com" {
		t.Fatalf("github: %s, %q", dest, whoami(b))
	}
	if !strings.Contains(mail.lastMail(t), "GitHub account that has your verified email address") {
		t.Errorf("no notice: %q", mail.lastMail(t))
	}
	// An unverified address joins nothing and makes nothing: anyone can type
	// alice's address into a new Discord account.
	b = newBrowser(t, srv, appOrigin)
	mallory := auth.Identity{Subject: "d-mallory", Email: "alice@example.com", EmailVerified: false}
	if dest := signIn(b, "discord", grant{id: mallory}); dest != "/signin?error=oauth_unverified" || whoami(b) != "" {
		t.Errorf("unverified address: %s, %q", dest, whoami(b))
	}

	// Login CSRF: a callback from a flow this browser didn't start.
	u, _ := begin(newBrowser(t, srv, appOrigin), "google", false)
	state, code2 := p.authorize(t, u, grant{id: carol})
	victim := newBrowser(t, srv, appOrigin)
	if dest := back(victim, "google", state, code2); dest != "/signin?error=oauth_failed" || whoami(victim) != "" {
		t.Errorf("someone else's callback: %s", dest)
	}
	// A callback works once.
	b = newBrowser(t, srv, appOrigin)
	u, _ = begin(b, "google", false)
	state, code2 = p.authorize(t, u, grant{id: carol})
	if dest := back(b, "google", state, code2); dest != "/" {
		t.Fatal(dest)
	}
	if dest := back(b, "google", state, code2); dest != "/signin?error=oauth_failed" {
		t.Errorf("replayed callback: %s", dest)
	}
	// The user saying no.
	c := *b.http
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(srv.URL + "/oauth/google/callback?error=access_denied&state=x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("Location") != appOrigin+"/signin?error=oauth_cancelled" {
		t.Errorf("cancelled: %s", resp.Header.Get("Location"))
	}
	// Flows expire.
	b = newBrowser(t, srv, appOrigin)
	u, _ = begin(b, "google", false)
	state, code2 = p.authorize(t, u, grant{id: carol})
	now = time.Now().Add(auth.OAuthFlowTTL + time.Minute)
	if dest := back(b, "google", state, code2); dest != "/signin?error=oauth_failed" {
		t.Errorf("expired flow: %s", dest)
	}
	now = time.Time{}

	// Linking, from alice's account settings: needs a recent re-auth, then
	// any provider account (verified or not) can be added.
	if _, err := begin(newBrowser(t, srv, appOrigin), "discord", true); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("link signed out: %v", err)
	}
	u, err = begin(alice, "discord", true)
	if err != nil {
		t.Fatal(err)
	}
	aliceDiscord := auth.Identity{Subject: "d-alice", Email: "alice@discord.example", EmailVerified: false}
	state, code2 = p.authorize(t, u, grant{id: aliceDiscord})
	if dest := back(alice, "discord", state, code2); dest != "/settings?linked=discord" {
		t.Fatalf("link: %s", dest)
	}
	list, err := alice.auth.ListOAuthAccounts(ctx, &panelv1.ListOAuthAccountsRequest{})
	if err != nil || len(list.GetAccounts()) != 2 {
		t.Fatalf("linked accounts: %v, %v", list, err)
	}
	// Now it signs in, whatever its email says.
	b = newBrowser(t, srv, appOrigin)
	if dest := signIn(b, "discord", grant{id: aliceDiscord}); dest != "/" || whoami(b) != "alice@example.com" {
		t.Errorf("linked discord: %s, %q", dest, whoami(b))
	}
	// A provider account can't be on two Raptor accounts.
	u, _ = begin(alice, "google", true)
	state, code2 = p.authorize(t, u, grant{id: carol})
	if dest := back(alice, "google", state, code2); dest != "/settings?error=oauth_taken" {
		t.Errorf("someone else's google: %s", dest)
	}

	// With TOTP on, a provider's sign-in is only the first step.
	setup, err := alice.auth.BeginTOTPSetup(ctx, &panelv1.BeginTOTPSetupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	totpNow := totpAt(t, setup.GetSecret(), time.Now())
	if _, err := alice.auth.FinishTOTPSetup(ctx, &panelv1.FinishTOTPSetupRequest{Code: totpNow}); err != nil {
		t.Fatal(err)
	}
	b = newBrowser(t, srv, appOrigin)
	if dest := signIn(b, "discord", grant{id: aliceDiscord}); dest != "/signin/second-factor" || whoami(b) != "" {
		t.Errorf("oauth with TOTP: %s, %q", dest, whoami(b))
	}
	if _, err := b.auth.FinishSecondFactor(ctx, &panelv1.FinishSecondFactorRequest{Proof: &panelv1.FinishSecondFactorRequest_TotpCode{
		TotpCode: totpAt(t, setup.GetSecret(), time.Now().Add(30*time.Second)),
	}}); err != nil || whoami(b) != "alice@example.com" {
		t.Errorf("second factor after oauth: %v", err)
	}

	// Unlinking needs a re-auth, and someone else's can't be unlinked.
	id := list.GetAccounts()[1].GetId()
	carolB := newBrowser(t, srv, appOrigin)
	signIn(carolB, "google", grant{id: carol})
	if _, err := carolB.auth.UnlinkOAuthAccount(ctx, &panelv1.UnlinkOAuthAccountRequest{Id: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("someone else's unlinked: %v", err)
	}
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	if _, err := alice.auth.UnlinkOAuthAccount(ctx, &panelv1.UnlinkOAuthAccountRequest{Id: id}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("unlink without re-auth: %v", err)
	}
	now = time.Time{}
	if _, err := alice.auth.UnlinkOAuthAccount(ctx, &panelv1.UnlinkOAuthAccountRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	b = newBrowser(t, srv, appOrigin)
	if dest := signIn(b, "discord", grant{id: aliceDiscord}); dest != "/signin?error=oauth_unverified" {
		t.Errorf("unlinked discord: %s", dest)
	}
	if err := svc.Prune(ctx); err != nil {
		t.Error(err)
	}
}

func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
