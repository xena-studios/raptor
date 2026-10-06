package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// OAuthFlowTTL is how long a trip to a provider may take.
const OAuthFlowTTL = 10 * time.Minute

// oauthCookie holds a flow's state. It's SameSite=Lax, unlike the session,
// because the provider sends the browser back from another site.
const oauthCookie = "__Host-raptor_oauth"

var providerNames = map[string]string{"google": "Google", "github": "GitHub", "discord": "Discord"}

// GetSignInMethods implements AuthService.
func (s *Service) GetSignInMethods(context.Context, *panelv1.GetSignInMethodsRequest) (*panelv1.GetSignInMethodsResponse, error) {
	out := &panelv1.GetSignInMethodsResponse{Email: s.Mailer != nil, Passkeys: s.WebAuthn != nil}
	for name := range s.OAuth {
		out.OauthProviders = append(out.OauthProviders, name)
	}
	slices.Sort(out.OauthProviders)
	return out, nil
}

// BeginOAuth implements AuthService.
func (s *Service) BeginOAuth(ctx context.Context, req *panelv1.BeginOAuthRequest) (*panelv1.BeginOAuthResponse, error) {
	p, ok := s.OAuth[req.GetProvider()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("signing in with %q isn't set up on this Panel", req.GetProvider()))
	}
	if err := s.limit(ctx, "ceremony:ip:"+s.clientIP(ctx).String(), limitCeremonyPerIP); err != nil {
		return nil, err
	}
	var session pgtype.UUID
	if req.GetLink() {
		sess, err := s.Current(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.requireReauth(sess); err != nil {
			return nil, err
		}
		session = sess.ID
	}
	state, verifier, nonce := newToken(), oauth2.GenerateVerifier(), newToken()
	if err := s.q().CreateOAuthFlow(ctx, store.CreateOAuthFlowParams{
		StateHash: hash(state), Provider: req.GetProvider(), Verifier: verifier, Nonce: nonce, SessionID: session,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(OAuthFlowTTL), Valid: true},
	}); err != nil {
		return nil, err
	}
	if c, ok := callOf(ctx); ok {
		setOAuthCookie(c.resp, state, OAuthFlowTTL)
	}
	return &panelv1.BeginOAuthResponse{Url: p.AuthCodeURL(state, verifier, nonce)}, nil
}

func setOAuthCookie(h http.Header, state string, maxAge time.Duration) {
	c := &http.Cookie{
		Name: oauthCookie, Value: state, Path: "/", MaxAge: int(maxAge.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	if maxAge <= 0 {
		c.MaxAge = -1
	}
	h.Add("Set-Cookie", c.String())
}

// OAuthCallback serves GET /oauth/{provider}/callback: the provider sends
// the browser here, and it leaves for the web app with a session (or a
// second-factor prompt, or an error code in the URL).
func (s *Service) OAuthCallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withHTTPCall(r.Context(), w, r)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		setOAuthCookie(w.Header(), "", 0)
		dest := s.oauthCallback(ctx, r)
		// dest is always one of oauthCallback's own paths, on the web app.
		http.Redirect(w, r, strings.TrimSuffix(s.AppURL, "/")+dest, http.StatusSeeOther) //nolint:gosec // not user-controlled
	})
}

// oauthCallback does the work and returns where in the web app to go.
func (s *Service) oauthCallback(ctx context.Context, r *http.Request) string {
	provider := r.PathValue("provider")
	p, ok := s.OAuth[provider]
	if !ok {
		return "/signin?error=oauth_failed"
	}
	if err := s.limit(ctx, "check:ip:"+s.clientIP(ctx).String(), limitCheckPerIP); err != nil {
		return "/signin?error=rate_limited"
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		return "/signin?error=oauth_cancelled"
	}
	// The state must come back in the URL and match the cookie this browser
	// got when it left: a callback from someone else's flow (login CSRF)
	// has no cookie to match.
	state := q.Get("state")
	ck, err := r.Cookie(oauthCookie)
	if state == "" || err != nil || subtle.ConstantTimeCompare([]byte(state), []byte(ck.Value)) != 1 {
		return "/signin?error=oauth_failed"
	}
	flow, err := s.q().TakeOAuthFlow(ctx, hash(state))
	if err != nil || flow.Provider != provider || !flow.ExpiresAt.Time.After(s.now()) {
		return "/signin?error=oauth_failed"
	}
	id, err := p.Identify(ctx, q.Get("code"), flow.Verifier, flow.Nonce)
	if err != nil {
		s.log().Info("oauth sign-in failed", "provider", provider, "err", err)
		return "/signin?error=oauth_failed"
	}
	if flow.SessionID.Valid {
		return s.oauthLink(ctx, provider, flow.SessionID, id)
	}
	return s.oauthSignIn(ctx, provider, id)
}

func (s *Service) oauthSignIn(ctx context.Context, provider string, id Identity) string {
	var dest string
	var linked *store.User
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var user store.User
		acct, err := q.OAuthAccount(ctx, store.OAuthAccountParams{Provider: provider, Subject: id.Subject})
		switch {
		case err == nil:
			if err := q.UseOAuthAccount(ctx, store.UseOAuthAccountParams{ID: acct.ID, Email: id.Email, EmailVerified: id.EmailVerified}); err != nil {
				return err
			}
			if user, err = q.GetUser(ctx, acct.UserID); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			// A new login: it joins (or makes) the account with its email,
			// but only if the provider has verified the address. Otherwise
			// anyone could add a victim's address to their own account there
			// and sign in as the victim here.
			email, ok := verifiedEmail(id)
			if !ok {
				dest = "/signin?error=oauth_unverified"
				return nil
			}
			user, err = q.GetUserByEmail(ctx, email)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if user, err = q.CreateUser(ctx, email); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				if err := q.VerifyUserEmail(ctx, user.ID); err != nil {
					return err
				}
				linked = &user
			}
			if _, err := q.CreateOAuthAccount(ctx, store.CreateOAuthAccountParams{
				UserID: user.ID, Provider: provider, Subject: id.Subject, Email: email, EmailVerified: true,
			}); err != nil {
				return err
			}
		default:
			return err
		}
		// Like an email sign-in: only as strong as the provider's account,
		// so TOTP still applies, and it doesn't re-authenticate accounts
		// that have something stronger.
		if user.TotpEnabledAt.Valid {
			dest = "/signin/second-factor"
			return s.startPending(ctx, q, user)
		}
		strong, err := hasStrongMethod(ctx, q, user)
		if err != nil {
			return err
		}
		dest = "/"
		return s.startSession(ctx, q, user, !strong)
	})
	if err != nil {
		s.log().Error("oauth sign-in failed", "provider", provider, "err", err)
		return "/signin?error=oauth_failed"
	}
	if linked != nil {
		s.notify(ctx, *linked, providerNames[provider]+" sign-in was added to your Raptor account",
			fmt.Sprintf("Someone signed in to your Raptor account with a %s account that has your verified email address, so it can now sign in to your account.\n\nIf this wasn't you, remove it from your account settings and sign out every device right away.\n", providerNames[provider]))
	}
	return dest
}

// oauthLink adds a provider's account to the account whose session started
// the flow. The flow is bound to that session, and to this browser by the
// state cookie; the session cookie itself doesn't come along, since the
// browser arrives from another site.
func (s *Service) oauthLink(ctx context.Context, provider string, session pgtype.UUID, id Identity) string {
	const settings = "/settings/security"
	q := s.q()
	sess, err := q.GetSession(ctx, session)
	if err != nil || sess.RevokedAt.Valid || !sess.ExpiresAt.Time.After(s.now()) {
		return "/signin?error=oauth_failed"
	}
	user, err := q.GetUser(ctx, sess.UserID)
	if err != nil {
		return settings + "?error=oauth_failed"
	}
	if acct, err := q.OAuthAccount(ctx, store.OAuthAccountParams{Provider: provider, Subject: id.Subject}); err == nil {
		if acct.UserID != user.ID {
			return settings + "?error=oauth_taken"
		}
		return settings + "?linked=" + url.QueryEscape(provider)
	}
	email, _ := normalizeEmail(id.Email)
	_, err = q.CreateOAuthAccount(ctx, store.CreateOAuthAccountParams{
		UserID: user.ID, Provider: provider, Subject: id.Subject, Email: email, EmailVerified: id.EmailVerified,
	})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return settings + "?error=oauth_taken"
	}
	if err != nil {
		s.log().Error("linking an oauth account failed", "provider", provider, "err", err)
		return settings + "?error=oauth_failed"
	}
	s.notify(ctx, user, providerNames[provider]+" sign-in was added to your Raptor account",
		fmt.Sprintf("A %s account can now sign in to your Raptor account.\n\nIf this wasn't you, remove it from your account settings and sign out every device right away.\n", providerNames[provider]))
	return settings + "?linked=" + url.QueryEscape(provider)
}

// verifiedEmail is the identity's address, if the provider verified it.
func verifiedEmail(id Identity) (string, bool) {
	email, err := normalizeEmail(id.Email)
	return email, err == nil && id.EmailVerified
}

// ListOAuthAccounts implements AuthService.
func (s *Service) ListOAuthAccounts(ctx context.Context, _ *panelv1.ListOAuthAccountsRequest) (*panelv1.ListOAuthAccountsResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q().ListOAuthAccounts(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListOAuthAccountsResponse{}
	for _, r := range rows {
		a := &panelv1.OAuthAccount{Id: uuid.UUID(r.ID.Bytes).String(), Provider: r.Provider, Email: r.Email, CreatedAt: timestamppb.New(r.CreatedAt.Time)}
		if r.LastUsedAt.Valid {
			a.LastUsedAt = timestamppb.New(r.LastUsedAt.Time)
		}
		out.Accounts = append(out.Accounts, a)
	}
	return out, nil
}

// UnlinkOAuthAccount implements AuthService. An email code always works, so
// this never locks anyone out.
func (s *Service) UnlinkOAuthAccount(ctx context.Context, req *panelv1.UnlinkOAuthAccountRequest) (*panelv1.UnlinkOAuthAccountResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	row, err := s.q().DeleteOAuthAccount(ctx, store.DeleteOAuthAccountParams{ID: id, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such linked account"))
	}
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, providerNames[row.Provider]+" sign-in was removed from your Raptor account",
		fmt.Sprintf("A %s account can no longer sign in to your Raptor account.\n\nIf this wasn't you, sign out every device from your account settings right away.\n", providerNames[row.Provider]))
	return &panelv1.UnlinkOAuthAccountResponse{}, nil
}
