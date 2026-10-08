// Package auth signs people in to the Panel (docs/PANEL.md#auth):
// passwordless, with sessions in a host-only cookie. Any session can send
// commands to nodes where Wings runs as root, so this is the most sensitive
// code in the Panel: everything is rate limited, single use, short-lived,
// and stored hashed.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Service is the AuthService handler and the session check other services
// use.
type Service struct {
	DB     *pgxpool.Pool
	Mailer Mailer
	// Turnstile checks the "email me a code" form (nil: not checked, as in
	// development).
	Turnstile Verifier
	// AppURL is the web app's origin, for sign-in links
	// (https://app.raptorpanel.net).
	AppURL string
	// ClientIPHeader is trusted for the client's address when set (see
	// nodes.ClientIP).
	ClientIPHeader string
	// WebAuthn is the passkey relying party (NewWebAuthn); nil turns
	// passkeys off.
	WebAuthn *webauthn.WebAuthn
	// DataKey encrypts TOTP secrets (32 bytes, from PANEL_DATA_KEY); nil
	// turns TOTP off.
	DataKey []byte
	// OAuth are the providers people can sign in with, by name ("google",
	// "github", "discord").
	OAuth map[string]OAuthProvider
	Log   *slog.Logger
	Now   func() time.Time
}

func (s *Service) q() *store.Queries { return store.New(s.DB) }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// Lifetimes.
const (
	CodeTTL      = 10 * time.Minute
	CodeAttempts = 5
	SessionIdle  = 30 * 24 * time.Hour
	SessionMax   = 90 * 24 * time.Hour
)

// newToken is 32 random bytes, base64url.
func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// newCode is a uniformly random 6-digit code.
func newCode() string {
	const n = 1_000_000
	limit := ^uint32(0) - ^uint32(0)%n
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		v := binary.BigEndian.Uint32(b[:])
		if v < limit {
			s := "000000" + itoa(int(v%n))
			return s[len(s)-6:]
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// normalizeEmail lowercases and checks an address enough to send to it:
// something@a.domain, with no spaces, control characters, or characters
// that mean something in a mail header.
func normalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	bad := connect.NewError(connect.CodeInvalidArgument, errors.New("that doesn't look like an email address"))
	at := strings.LastIndex(e, "@")
	if len(e) > 254 || at < 1 || !utf8.ValidString(e) ||
		strings.ContainsFunc(e, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("<>,;:\"()[]\\", r)
		}) {
		return "", bad
	}
	labels := strings.Split(e[at+1:], ".")
	if len(labels) < 2 || strings.Contains(e[:at], "@") {
		return "", bad
	}
	for _, l := range labels {
		if l == "" {
			return "", bad
		}
	}
	return e, nil
}

// call is the HTTP exchange being served: a Connect call, or a plain
// request (the OAuth callback), so cookies work the same in both.
type call struct {
	req, resp http.Header
	peer      string
}

type httpCallKey struct{}

// withHTTPCall makes a plain request's headers available to callOf.
func withHTTPCall(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	return context.WithValue(ctx, httpCallKey{}, call{req: r.Header, resp: w.Header(), peer: r.RemoteAddr})
}

// WithRequest makes a plain HTTP request's cookies and address available
// to Current and AsUser, for handlers outside Connect (the live WebSocket).
func WithRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	return withHTTPCall(ctx, w, r)
}

func callOf(ctx context.Context) (call, bool) {
	if ci, ok := connect.CallInfoForHandlerContext(ctx); ok {
		return call{req: ci.RequestHeader(), resp: ci.ResponseHeader(), peer: ci.Peer().Addr}, true
	}
	c, ok := ctx.Value(httpCallKey{}).(call)
	return c, ok
}

// clientIP is the caller's address: the trusted header if configured,
// otherwise the TCP peer.
func (s *Service) clientIP(ctx context.Context) netip.Addr {
	c, ok := callOf(ctx)
	if !ok {
		return netip.Addr{}
	}
	if s.ClientIPHeader != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(c.req.Get(s.ClientIPHeader))); err == nil {
			return a.Unmap()
		}
	}
	host, _, err := net.SplitHostPort(c.peer)
	if err != nil {
		host = c.peer
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// Errors people see.
var (
	errSignedOut  = connect.NewError(connect.CodeUnauthenticated, errors.New("signed out"))
	errRateLimit  = connect.NewError(connect.CodeResourceExhausted, errors.New("too many attempts; wait a while and try again"))
	errBadCode    = connect.NewError(connect.CodePermissionDenied, errors.New("that code is wrong or expired; ask for a new one"))
	errBadLink    = connect.NewError(connect.CodePermissionDenied, errors.New("that link is used or expired; ask for a new one"))
	errChallenged = connect.NewError(connect.CodePermissionDenied, errors.New("the security check failed; reload the page and try again"))
	errBadPasskey = connect.NewError(connect.CodePermissionDenied, errors.New("that passkey didn't work; try again"))
	errNoPasskey  = connect.NewError(connect.CodeNotFound, errors.New("no such passkey"))
	errReauth     = connect.NewError(connect.CodeFailedPrecondition, errors.New("confirm it's you first"))
	errBadTOTP    = connect.NewError(connect.CodePermissionDenied, errors.New("that code is wrong; check the time on your phone and try the next one"))
	errNoPending  = connect.NewError(connect.CodeUnauthenticated, errors.New("that sign-in expired; start again"))
)

// For other Panel services.

// RequireReauth refuses a sensitive change unless the session
// re-authenticated in the last 5 minutes (FAILED_PRECONDITION).
func (s *Service) RequireReauth(sess *Session) error { return s.requireReauth(sess) }

// RateLimit counts an event for key and refuses (RESOURCE_EXHAUSTED) once
// there are more than max in window, across every Panel instance.
func (s *Service) RateLimit(ctx context.Context, key string, max int64, window time.Duration) error {
	return s.limit(ctx, key, Limit{max, window})
}

// NormalizeEmail lowercases and checks an address (INVALID_ARGUMENT).
func NormalizeEmail(e string) (string, error) { return normalizeEmail(e) }

// NewToken is 32 random bytes, base64url; HashToken is how to store one.
func NewToken() string { return newToken() }

// HashToken hashes a token for storage.
func HashToken(t string) []byte { return hash(t) }
