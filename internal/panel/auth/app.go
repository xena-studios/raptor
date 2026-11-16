package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/mail"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Raptor's phone app (docs/PANEL.md#the-phone-app) signs in like an OAuth
// 2.1 public client: it opens the web app in the system browser, the
// signed-in user approves it there (ApproveApp, after confirming it's
// them), the browser hands the code back to the app, and the app trades it
// with its PKCE verifier at POST /oauth/token for an access token and a
// refresh token. Both belong to a session row, so the phone is listed with
// the user's devices and signing it out there ends it.
//
// An app session can't re-authenticate, so it never gets anything that
// needs a recent passkey or confirmation: sensitive account changes, staff
// tools, support access, approving another app. Passkey-signed commands
// are refused from it too (DECISIONS: tokens never authorize signed
// actions); those stay in the web app.

// AppClient is an app that may sign in this way.
type AppClient struct {
	ID   string
	Name string
	// RedirectURIs are where the web app may send the code: exact matches.
	RedirectURIs []string
}

// AppClients are the apps that can sign in. Only Raptor's own.
var AppClients = map[string]AppClient{
	"raptor-mobile": {ID: "raptor-mobile", Name: "Raptor for iOS and Android", RedirectURIs: []string{"net.raptorpanel.app:/oauth"}},
}

// Lifetimes of the app's credentials. The session itself ends after
// SessionIdle without a refresh, like a browser's, but has no SessionMax:
// people don't expect to sign in to an app again every 90 days.
const (
	AppCodeTTL   = 2 * time.Minute
	AppAccessTTL = time.Hour
)

// AppTokenPath is the token endpoint, outside /api: the app isn't a browser.
const AppTokenPath = "/oauth/token" //nolint:gosec // a path, not a credential

var (
	limitApprovePerUser = Limit{20, time.Hour}
	limitTokenPerIP     = Limit{600, time.Hour}

	errAppOnlyInBrowser = connect.NewError(connect.CodePermissionDenied, errors.New("this can only be done in the web app, at app.raptorpanel.net"))
)

// App reports whether the session is the phone app's, not a browser's.
func (s *Session) App() bool { return s.Client != "" }

// RefuseApp refuses an app session: for things only the web app may do.
func RefuseApp(sess *Session) error {
	if sess.App() {
		return errAppOnlyInBrowser
	}
	return nil
}

// bearerToken is the request's Authorization: Bearer token, if it sent
// one. ok is true whenever there's an Authorization header, so a malformed
// one signs nobody in rather than falling back to a cookie.
func bearerToken(h http.Header) (token string, ok bool) {
	v := h.Get("Authorization")
	if v == "" {
		return "", false
	}
	scheme, token, found := strings.Cut(v, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(token), true
}

// lookupApp checks an app's access token.
func (s *Service) lookupApp(ctx context.Context, token string) (*Session, error) {
	sess, err := s.lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	if !sess.App() || !sess.AccessExpiresAt.Valid || !sess.AccessExpiresAt.Time.After(s.now()) {
		return nil, errSignedOut
	}
	return sess, nil
}

// cleanDeviceName keeps a phone's name printable and short.
func cleanDeviceName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || !unicode.IsPrint(r) && r != ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(n))
	if utf8.RuneCountInString(n) > 64 {
		n = string([]rune(n)[:64])
	}
	if n = strings.TrimSpace(n); n == "" {
		return "Phone"
	}
	return n
}

// validChallenge is an S256 PKCE challenge: 32 bytes, base64url, unpadded.
func validChallenge(c string) bool {
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// ApproveApp implements AuthService.
func (s *Service) ApproveApp(ctx context.Context, req *panelv1.ApproveAppRequest) (*panelv1.ApproveAppResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := RefuseApp(sess); err != nil {
		return nil, err
	}
	client, ok := AppClients[req.GetClientId()]
	if !ok || !slices.Contains(client.RedirectURIs, req.GetRedirectUri()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("that isn't Raptor's app; open the sign-in from the app again"))
	}
	if !validChallenge(req.GetCodeChallenge()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the app's sign-in request is incomplete; open it from the app again"))
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "approve-app:user:"+uuid.UUID(sess.UserID.Bytes).String(), limitApprovePerUser); err != nil {
		return nil, err
	}
	code := newToken()
	if err := s.q().CreateAppCode(ctx, store.CreateAppCodeParams{
		CodeHash: hash(code), UserID: sess.UserID, Client: client.ID, RedirectUri: req.GetRedirectUri(),
		Challenge: req.GetCodeChallenge(), DeviceName: cleanDeviceName(req.GetDeviceName()),
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(AppCodeTTL), Valid: true},
	}); err != nil {
		return nil, err
	}
	return &panelv1.ApproveAppResponse{Code: code}, nil
}

// tokenError is an OAuth error response (RFC 6749 §5.2).
type tokenError struct {
	status int
	code   string
	desc   string
}

var (
	errInvalidGrant   = &tokenError{http.StatusBadRequest, "invalid_grant", "the sign-in expired or was already used; sign in again"}
	errInvalidRequest = &tokenError{http.StatusBadRequest, "invalid_request", ""}
	errInvalidClient  = &tokenError{http.StatusUnauthorized, "invalid_client", ""}
	errUnsupported    = &tokenError{http.StatusBadRequest, "unsupported_grant_type", ""}
	errTokenRate      = &tokenError{http.StatusTooManyRequests, "slow_down", "too many attempts; wait a while and try again"}
)

// tokenResponse is what the app gets (RFC 6749 §5.1).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// AppToken serves POST /oauth/token: grant_type authorization_code (a code
// from ApproveApp and the PKCE verifier) or refresh_token. A refresh token
// works once; using one again ends the session, since it means it was
// copied (RFC 9700 §4.14.2).
func (s *Service) AppToken() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// The app isn't a browser; no web page gets to call this.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browsers can't use this", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		ctx := withHTTPCall(r.Context(), w, r)
		res, terr := s.appToken(ctx, r)
		w.Header().Set("Content-Type", "application/json")
		if terr != nil {
			w.WriteHeader(terr.status)
			out := map[string]string{"error": terr.code}
			if terr.desc != "" {
				out["error_description"] = terr.desc
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		_ = json.NewEncoder(w).Encode(res) //nolint:gosec // handing the app its tokens is the point
	})
}

func (s *Service) appToken(ctx context.Context, r *http.Request) (*tokenResponse, *tokenError) {
	if ok, err := s.allow(ctx, "token:ip:"+s.clientIP(ctx).String(), limitTokenPerIP); err != nil {
		return nil, &tokenError{http.StatusInternalServerError, "server_error", ""}
	} else if !ok {
		return nil, errTokenRate
	}
	if err := r.ParseForm(); err != nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return nil, errInvalidRequest
	}
	client, ok := AppClients[r.PostForm.Get("client_id")]
	if !ok {
		return nil, errInvalidClient
	}
	var res *tokenResponse
	var terr *tokenError
	var after func()
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			res, after, terr, err = s.exchangeAppCode(ctx, q, client, r.PostForm)
		case "refresh_token":
			res, terr, err = s.refreshApp(ctx, q, client, r.PostForm.Get("refresh_token"))
		default:
			terr = errUnsupported
		}
		return err
	})
	if err != nil {
		s.log().Error("app token", "err", err)
		return nil, &tokenError{http.StatusInternalServerError, "server_error", ""}
	}
	if terr == nil && after != nil {
		after()
	}
	return res, terr
}

// newAppTokens makes an access token and a refresh token.
func (s *Service) newAppTokens() (access, refresh string, accessUntil, idleUntil pgtype.Timestamptz) {
	now := s.now()
	return newToken(), newToken(), pgtype.Timestamptz{Time: now.Add(AppAccessTTL), Valid: true},
		pgtype.Timestamptz{Time: now.Add(SessionIdle), Valid: true}
}

func (s *Service) callerDetails(ctx context.Context) (*netip.Addr, string) {
	var ip *netip.Addr
	if a := s.clientIP(ctx); a.IsValid() {
		ip = &a
	}
	ua := ""
	if c, ok := callOf(ctx); ok {
		ua = c.req.Get("User-Agent")
		if len(ua) > 256 {
			ua = ua[:256]
		}
	}
	return ip, ua
}

func (s *Service) exchangeAppCode(ctx context.Context, q *store.Queries, client AppClient, f map[string][]string) (*tokenResponse, func(), *tokenError, error) {
	get := func(k string) string {
		if v := f[k]; len(v) == 1 {
			return v[0]
		}
		return ""
	}
	code, verifier := get("code"), get("code_verifier")
	if code == "" || verifier == "" {
		return nil, nil, errInvalidRequest, nil
	}
	row, err := q.TakeAppCode(ctx, hash(code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, errInvalidGrant, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if row.Client != client.ID || row.RedirectUri != get("redirect_uri") || !row.ExpiresAt.Time.After(s.now()) ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(row.Challenge)) != 1 {
		return nil, nil, errInvalidGrant, nil
	}
	user, err := q.GetUser(ctx, row.UserID)
	if err != nil {
		return nil, nil, nil, err
	}
	if user.SuspendedAt.Valid {
		return nil, nil, errInvalidGrant, nil
	}
	access, refresh, accessUntil, idleUntil := s.newAppTokens()
	ip, ua := s.callerDetails(ctx)
	sess, err := q.CreateAppSession(ctx, store.CreateAppSessionParams{
		UserID: user.ID, TokenHash: hash(access), ExpiresAt: idleUntil, Ip: ip, UserAgent: ua,
		Client: client.ID, DeviceName: row.DeviceName, AccessExpiresAt: accessUntil, RefreshHash: hash(refresh),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := s.Audit(ctx, q, Event{
		User: user.ID, Action: "signin", Target: uuid.UUID(sess.ID.Bytes).String(),
		Meta: map[string]any{"method": "app", "client": client.ID, "device": row.DeviceName},
	}); err != nil {
		return nil, nil, nil, err
	}
	after := func() {
		s.notify(ctx, user, mail.Email{
			Subject: "Raptor's app signed in to your account",
			Title:   "Raptor's app signed in",
			Body:    []string{"Raptor's phone app was just signed in to your account. It can see your orgs and servers, start and stop them, and get your alerts."},
			Facts:   []mail.Fact{{Label: "Device", Value: row.DeviceName, Mono: true}, {Label: "When", Value: s.now().UTC().Format("2 Jan 2006 15:04 UTC"), Mono: true}},
			Button:  s.securityLink(),
			Note:    "If this wasn't you, sign that device out in your security settings and check your sign-in methods right away.",
		})
	}
	return &tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AppAccessTTL.Seconds()), RefreshToken: refresh}, after, nil, nil
}

func (s *Service) refreshApp(ctx context.Context, q *store.Queries, client AppClient, refresh string) (*tokenResponse, *tokenError, error) {
	if refresh == "" {
		return nil, errInvalidRequest, nil
	}
	sess, err := q.AppSessionByRefresh(ctx, hash(refresh))
	if errors.Is(err, pgx.ErrNoRows) {
		// A refresh token that was already used: someone else has a copy.
		// End the session, so neither copy works.
		old, err := q.AppSessionByPreviousRefresh(ctx, hash(refresh))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errInvalidGrant, nil
		}
		if err != nil {
			return nil, nil, err
		}
		if !old.RevokedAt.Valid {
			if err := q.RevokeSession(ctx, store.RevokeSessionParams{ID: old.ID, UserID: old.UserID}); err != nil {
				return nil, nil, err
			}
			if err := s.Audit(ctx, q, Event{
				User: old.UserID, Action: "session.revoke", Target: uuid.UUID(old.ID.Bytes).String(),
				Meta: map[string]any{"reason": "refresh_reused"},
			}); err != nil {
				return nil, nil, err
			}
		}
		return nil, errInvalidGrant, nil
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if sess.Client != client.ID || sess.RevokedAt.Valid || !sess.ExpiresAt.Time.After(now) || now.Sub(sess.LastSeenAt.Time) > SessionIdle {
		return nil, errInvalidGrant, nil
	}
	user, err := q.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.SuspendedAt.Valid {
		return nil, errInvalidGrant, nil
	}
	access, next, accessUntil, idleUntil := s.newAppTokens()
	ip, ua := s.callerDetails(ctx)
	if err := q.RotateAppSession(ctx, store.RotateAppSessionParams{
		ID: sess.ID, TokenHash: hash(access), AccessExpiresAt: accessUntil, RefreshHash: hash(next),
		PreviousRefreshHash: hash(refresh), ExpiresAt: idleUntil, Ip: ip, UserAgent: ua,
		LastSeenAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return nil, nil, err
	}
	return &tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AppAccessTTL.Seconds()), RefreshToken: next}, nil, nil
}
