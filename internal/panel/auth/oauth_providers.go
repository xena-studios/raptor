package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Identity is who a provider says signed in.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
}

// OAuthProvider is an account system people can sign in with.
type OAuthProvider interface {
	// AuthCodeURL is where to send the browser, with PKCE and (for OpenID
	// Connect) a nonce.
	AuthCodeURL(state, verifier, nonce string) string
	// Identify trades the code the browser came back with for who signed in.
	Identify(ctx context.Context, code, verifier, nonce string) (Identity, error)
}

// providerTimeout bounds every call to a provider.
const providerTimeout = 15 * time.Second

func providerContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: providerTimeout}), cancel
}

// OIDC is an OpenID Connect provider: Google.
type OIDC struct {
	Config   oauth2.Config
	Verifier *oidc.IDTokenVerifier
}

// Google signs in with Google. The endpoints are Google's published ones,
// so starting the Panel doesn't depend on reaching Google.
func Google(clientID, secret, redirectURL string) *OIDC {
	p := (&oidc.ProviderConfig{ //nolint:gosec // public endpoints, not credentials
		IssuerURL:  "https://accounts.google.com",
		AuthURL:    "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:   "https://oauth2.googleapis.com/token",
		JWKSURL:    "https://www.googleapis.com/oauth2/v3/certs",
		Algorithms: []string{oidc.RS256},
	}).NewProvider(context.Background())
	return NewOIDC(p, clientID, secret, redirectURL)
}

// NewOIDC is an OpenID Connect provider.
func NewOIDC(p *oidc.Provider, clientID, secret, redirectURL string) *OIDC {
	return &OIDC{
		Config: oauth2.Config{
			ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL,
			Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email"},
		},
		Verifier: p.Verifier(&oidc.Config{ClientID: clientID}),
	}
}

// AuthCodeURL implements OAuthProvider.
func (o *OIDC) AuthCodeURL(state, verifier, nonce string) string {
	return o.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
}

// Identify implements OAuthProvider: the ID token, checked against the
// provider's keys, this client, and the nonce.
func (o *OIDC) Identify(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	ctx, cancel := providerContext(ctx)
	defer cancel()
	tok, err := o.Config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, err
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return Identity{}, errors.New("no ID token")
	}
	idt, err := o.Verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	if idt.Nonce != nonce {
		return Identity{}, errors.New("ID token nonce doesn't match")
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&c); err != nil {
		return Identity{}, err
	}
	return Identity{Subject: idt.Subject, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}

// GitHub signs in with GitHub.
type GitHub struct {
	Config oauth2.Config
	API    string // https://api.github.com
}

// NewGitHub is GitHub's OAuth app.
func NewGitHub(clientID, secret, redirectURL string) *GitHub {
	return &GitHub{
		Config: oauth2.Config{
			ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL, Scopes: []string{"user:email"},
			Endpoint: oauth2.Endpoint{AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token"}, //nolint:gosec // public endpoints
		},
		API: "https://api.github.com",
	}
}

// AuthCodeURL implements OAuthProvider.
func (g *GitHub) AuthCodeURL(state, verifier, _ string) string {
	return g.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Identify implements OAuthProvider: the user's ID, and their primary email
// with whether GitHub has verified it.
func (g *GitHub) Identify(ctx context.Context, code, verifier, _ string) (Identity, error) {
	ctx, cancel := providerContext(ctx)
	defer cancel()
	tok, err := g.Config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, err
	}
	client := g.Config.Client(ctx, tok)
	var user struct {
		ID int64 `json:"id"`
	}
	if err := getJSON(ctx, client, g.API+"/user", &user); err != nil {
		return Identity{}, err
	}
	if user.ID == 0 {
		return Identity{}, errors.New("no user ID")
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, g.API+"/user/emails", &emails); err != nil {
		return Identity{}, err
	}
	id := Identity{Subject: strconv.FormatInt(user.ID, 10)}
	for _, e := range emails {
		if e.Primary {
			id.Email, id.EmailVerified = e.Email, e.Verified
		}
	}
	return id, nil
}

// Discord signs in with Discord.
type Discord struct {
	Config oauth2.Config
	API    string // https://discord.com/api
}

// NewDiscord is Discord's OAuth app.
func NewDiscord(clientID, secret, redirectURL string) *Discord {
	return &Discord{
		Config: oauth2.Config{
			ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL, Scopes: []string{"identify", "email"},
			Endpoint: oauth2.Endpoint{AuthURL: "https://discord.com/oauth2/authorize", TokenURL: "https://discord.com/api/oauth2/token"}, //nolint:gosec // public endpoints
		},
		API: "https://discord.com/api",
	}
}

// AuthCodeURL implements OAuthProvider.
func (d *Discord) AuthCodeURL(state, verifier, _ string) string {
	return d.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Identify implements OAuthProvider.
func (d *Discord) Identify(ctx context.Context, code, verifier, _ string) (Identity, error) {
	ctx, cancel := providerContext(ctx)
	defer cancel()
	tok, err := d.Config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, err
	}
	var user struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, d.Config.Client(ctx, tok), d.API+"/users/@me", &user); err != nil {
		return Identity{}, err
	}
	if user.ID == "" {
		return Identity{}, errors.New("no user ID")
	}
	return Identity{Subject: user.ID, Email: user.Email, EmailVerified: user.Verified}, nil
}

func getJSON(ctx context.Context, c *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // the provider's API, from code
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req) //nolint:gosec // the provider's API, from code
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}
