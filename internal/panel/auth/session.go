package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// CookieName is the session cookie. The __Host- prefix makes browsers
// refuse it unless it's Secure, has Path=/, and has no Domain, so a sibling
// subdomain can't set or overwrite it (docs/DECISIONS.md #83).
const CookieName = "__Host-raptor_session"

// Session is a signed-in session.
type Session struct {
	store.Session
	User store.User
}

// startSession creates a session for a user, records the sign-in (method:
// "email", "passkey", "google", ...), and sets the cookie on the response.
// A new token every time: signing in never reuses one. reauthed says
// whether the sign-in also counts as a re-authentication. If the account
// hasn't been used from this browser before, after is the email that says
// so, for the caller to send once the transaction commits.
func (s *Service) startSession(ctx context.Context, q *store.Queries, user store.User, reauthed bool, method string) (after func(), err error) {
	token := newToken()
	var ip *netip.Addr
	if a := s.clientIP(ctx); a.IsValid() {
		ip = &a
	}
	ua := ""
	c, ok := callOf(ctx)
	if ok {
		ua = c.req.Get("User-Agent")
		if len(ua) > 256 {
			ua = ua[:256]
		}
	}
	// Signing in again in a browser ends the session it had: the old token
	// shouldn't outlive the cookie that held it.
	if ok {
		if old := cookieToken(c.req); old != "" {
			if err := q.RevokeSessionByToken(ctx, hash(old)); err != nil {
				return nil, err
			}
		}
	}
	seen, err := q.SessionsWithUserAgent(ctx, store.SessionsWithUserAgentParams{UserID: user.ID, UserAgent: ua})
	if err != nil {
		return nil, err
	}
	newDevice := seen.Total > 0 && seen.Same == 0
	sess, err := q.CreateSession(ctx, store.CreateSessionParams{
		UserID: user.ID, TokenHash: hash(token), ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(SessionMax), Valid: true},
		Ip: ip, UserAgent: ua, ReauthAt: pgtype.Timestamptz{Time: s.now(), Valid: reauthed},
	})
	if err != nil {
		return nil, err
	}
	if err := s.Audit(ctx, q, Event{
		User: user.ID, Action: "signin", Target: uuid.UUID(sess.ID.Bytes).String(),
		Meta: map[string]any{"method": method, "new_device": newDevice},
	}); err != nil {
		return nil, err
	}
	if ok {
		setCookie(c.resp, token, SessionMax)
	}
	if !newDevice {
		return func() {}, nil
	}
	where := "an unknown address"
	if ip != nil {
		where = ip.String()
	}
	return func() {
		s.notify(ctx, user, "New sign-in to your Raptor account",
			fmt.Sprintf("Your Raptor account was just signed in to from a device or browser it hasn't used before:\n\n%s\nfrom %s\n\nIf this was you, there's nothing to do. If it wasn't, sign out every device from your account settings and check your sign-in methods right away.\n", ua, where))
	}, nil
}

func setCookie(h http.Header, token string, maxAge time.Duration) {
	setNamedCookie(h, CookieName, token, maxAge)
}

// setNamedCookie sets a host-only, script-proof cookie (an empty token
// clears it).
func setNamedCookie(h http.Header, name, token string, maxAge time.Duration) {
	c := &http.Cookie{
		Name: name, Value: token, Path: "/", MaxAge: int(maxAge.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}
	if maxAge <= 0 {
		c.MaxAge = -1
	}
	h.Add("Set-Cookie", c.String())
}

// cookieToken reads the session token from a request's headers.
func cookieToken(h http.Header) string { return namedCookie(h, CookieName) }

func namedCookie(h http.Header, name string) string {
	r := http.Request{Header: h}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// Current returns the request's session, or UNAUTHENTICATED. Other services
// call it first thing.
func (s *Service) Current(ctx context.Context) (*Session, error) {
	c, ok := callOf(ctx)
	if !ok {
		return nil, errSignedOut
	}
	return s.lookup(ctx, cookieToken(c.req))
}

func (s *Service) lookup(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, errSignedOut
	}
	q := s.q()
	sess, err := q.SessionByToken(ctx, hash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSignedOut
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if sess.RevokedAt.Valid || !sess.ExpiresAt.Time.After(now) || now.Sub(sess.LastSeenAt.Time) > SessionIdle {
		return nil, errSignedOut
	}
	user, err := q.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	// Last seen to the minute is plenty, and saves a write per request.
	if now.Sub(sess.LastSeenAt.Time) > time.Minute {
		if err := q.TouchSession(ctx, sess.ID); err != nil {
			return nil, err
		}
	}
	return &Session{Session: sess, User: user}, nil
}
