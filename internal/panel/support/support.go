// Package support takes diagnostics bundles from nodes (raptor doctor
// -upload) and stores them for Raptor's support staff, who find one by the
// code it's given. The Panel can only write bundles, never read them: its
// storage key should allow uploads only (docs/DEPLOY.md#support-bundles).
package support

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// MaxBundle is the largest bundle taken. Doctor's are a few MB at most
// (logs are capped), so this only stops abuse.
const MaxBundle = 32 << 20

// Limits on uploads: a linked node (signed) gets a few a day; anyone else,
// fewer per address; and all of them together are capped, so the bucket
// can't be filled.
var (
	LimitPerNode = Limit{10, 24 * time.Hour}
	LimitPerIP   = Limit{3, 24 * time.Hour}
	LimitTotal   = Limit{1000, 24 * time.Hour}
)

// Limit is at most N uploads in Window.
type Limit struct {
	N      int64
	Window time.Duration
}

type limit struct {
	key string
	Limit
}

// Store keeps bundles (object storage in production).
type Store interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) error
}

// RateLimiter counts events across Panel instances (auth.Service).
type RateLimiter interface {
	RateLimit(ctx context.Context, key string, max int64, window time.Duration) error
}

// Service handles uploads.
type Service struct {
	DB      *pgxpool.Pool
	Store   Store
	Limiter RateLimiter
	// ClientIPHeader is the header with the client's address, as for the
	// rest of the Panel (CF-Connecting-IP).
	ClientIPHeader string
	Log            *slog.Logger
	Now            func() time.Time

	slots chan struct{}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Response is what an upload answers.
type Response struct {
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

func reply(w http.ResponseWriter, status int, r Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}

// ServeHTTP takes one bundle: POST nodelink.BundlePath, the .tar.gz as the
// body, signed by the node's key if it's linked.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.slots == nil {
		s.slots = make(chan struct{}, 4)
	}
	if r.ContentLength > MaxBundle {
		reply(w, http.StatusRequestEntityTooLarge, Response{Error: "the bundle is too large"})
		return
	}
	// Bundles are held in memory while they're checked and stored: a few
	// at a time.
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		reply(w, http.StatusServiceUnavailable, Response{Error: "busy; try again in a minute"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBundle))
	if err != nil {
		reply(w, http.StatusRequestEntityTooLarge, Response{Error: "the bundle is too large"})
		return
	}
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		reply(w, http.StatusBadRequest, Response{Error: "not a bundle (.tar.gz)"})
		return
	}
	ctx := r.Context()
	ip := nodes.ClientIP(r, s.ClientIPHeader)

	node := r.Header.Get(nodelink.BundleNodeHeader)
	if node != "" {
		if err := s.verify(ctx, r, node, body); err != nil {
			s.log().Info("support bundle refused", "node", node, "ip", ip, "err", err)
			reply(w, http.StatusUnauthorized, Response{Error: "the node's signature didn't check out: " + err.Error()})
			return
		}
	}
	// The node's or address's own limit first, so one that's over it
	// doesn't use up everyone's.
	limits := []limit{{"bundle:ip:" + ipKey(ip), LimitPerIP}, {"bundle:all", LimitTotal}}
	if node != "" {
		limits[0] = limit{"bundle:node:" + node, LimitPerNode}
	}
	for _, l := range limits {
		if err := s.Limiter.RateLimit(ctx, l.key, l.N, l.Window); err != nil {
			reply(w, http.StatusTooManyRequests, Response{Error: "too many bundles; send this one to support another way, or try tomorrow"})
			return
		}
	}

	code, err := NewCode()
	if err != nil {
		reply(w, http.StatusInternalServerError, Response{Error: "internal error"})
		return
	}
	sum := sha256.Sum256(body)
	now := s.now().UTC()
	meta := map[string]string{"ip": ip.String(), "sha256": hex.EncodeToString(sum[:])}
	if node != "" {
		meta["node"] = node
	}
	key := "bundles/" + now.Format("2006-01-02") + "/" + code + ".tar.gz"
	if err := s.Store.Put(ctx, key, body, meta); err != nil {
		s.log().Error("support bundle not stored", "code", code, "err", err)
		reply(w, http.StatusBadGateway, Response{Error: "couldn't store the bundle; try again later"})
		return
	}
	s.log().Info("support bundle", "code", code, "node", node, "ip", ip, "bytes", len(body), "key", key)
	reply(w, http.StatusOK, Response{Code: code})
}

// verify checks a signed upload against the node's key.
func (s *Service) verify(ctx context.Context, r *http.Request, node string, body []byte) error {
	id, err := uuid.Parse(node)
	if err != nil {
		return errors.New("unknown node")
	}
	n, err := store.New(s.DB).GetNode(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (n.KeyRevokedAt.Valid || n.DeletedAt.Valid)) {
		return errors.New("unknown node")
	}
	if err != nil {
		return err
	}
	return nodelink.VerifyBundle(ed25519.PublicKey(n.PublicKey), node, r.Header.Get(nodelink.BundleTimeHeader),
		r.Header.Get(nodelink.BundleSignatureHeader), s.now(), body)
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ipKey is the address an anonymous upload is counted by: IPv6 by /64,
// since one host usually has a whole one.
func ipKey(a netip.Addr) string {
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

// The code's alphabet: Crockford's base32, no I, L, O, or U, so it reads
// out loud and types without mix-ups.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewCode is a support code, RPT-XXXX-XXXX: 40 random bits, so codes don't
// collide and can't be guessed to find someone's bundle.
func NewCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	c := []byte("RPT-")
	for i, x := range b {
		if i == 4 {
			c = append(c, '-')
		}
		c = append(c, alphabet[x&31])
	}
	return string(c), nil
}
