package nodes

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// JoinTokenTTL is how long a join token works.
const JoinTokenTTL = time.Hour

// Registry is the Panel's record of nodes: join tokens, enrollment, and the
// keys the hub checks connections against.
type Registry struct {
	DB       *pgxpool.Pool
	PanelKey ed25519.PrivateKey
	Now      func() time.Time
	// KeyChanged is called after a node's key is replaced (re-linking), to
	// drop connections made with the old one.
	KeyChanged func(nodeID string)
}

func (r *Registry) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Registry) q() *store.Queries { return store.New(r.DB) }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// UUIDString formats a database UUID.
func UUIDString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateOrg adds an org with no members (`panel org create`, for
// development; users make orgs through OrgService).
func (r *Registry) CreateOrg(ctx context.Context, name string) (string, error) {
	o, err := r.q().CreateOrg(ctx, name)
	if err != nil {
		return "", err
	}
	return UUIDString(o.ID), nil
}

// CreateJoinToken makes a single-use token that enrolls one node into org.
// Only its hash is stored; the token is shown once.
func (r *Registry) CreateJoinToken(ctx context.Context, orgID, name string) (string, error) {
	id, err := uuid.Parse(orgID)
	if err != nil {
		return "", fmt.Errorf("org ID: %w", err)
	}
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := nodelink.JoinTokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	_, err = r.q().CreateJoinToken(ctx, store.CreateJoinTokenParams{
		OrgID: pgUUID(id), TokenHash: hashToken(token), Name: name,
		ExpiresAt: pgtype.Timestamptz{Time: r.now().Add(JoinTokenTTL), Valid: true},
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Short IDs: 8 characters from a lowercase alphabet without look-alikes
// (no 0/o, 1/l/i), random, never reused (docs/ARCHITECTURE.md#node-dns).
const shortIDAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newShortID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = shortIDAlphabet[int(b[i])%len(shortIDAlphabet)]
	}
	return string(b), nil
}

// Enrollment errors, shown to the person running `raptor link`.
var (
	ErrBadToken = errors.New("the join token is invalid, expired, or already used: make a new one in the Panel")
)

// Enroll trades a join token for a node. The token is burned in the same
// transaction the node is created in; repeating an enrollment with the same
// token and key returns the node it made.
func (r *Registry) Enroll(ctx context.Context, req *nodev1.EnrollRequest) (*nodev1.EnrollResponse, error) {
	key := req.GetPublicKey()
	if len(key) != ed25519.PublicKeySize {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("public key must be Ed25519"))
	}
	if !strings.HasPrefix(req.GetToken(), nodelink.JoinTokenPrefix) {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrBadToken)
	}
	payload, err := nodelink.EnrollPayload(req.GetToken(), key)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, payload, req.GetSignature()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the enrollment isn't signed by the key being enrolled"))
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" || !utf8.ValidString(name) {
		name = "node"
	}
	name = truncate(name, 64)
	facts, err := json.Marshal(req.GetFacts())
	if err != nil {
		return nil, err
	}
	var relink pgtype.UUID
	if req.GetNodeId() != "" {
		id, err := uuid.Parse(req.GetNodeId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad node ID"))
		}
		relink = pgUUID(id)
	}

	var node store.Node
	var pin []byte
	err = pgx.BeginFunc(ctx, r.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		jt, err := q.GetJoinTokenForUpdate(ctx, hashToken(req.GetToken()))
		if err == nil {
			pin = jt.OwnerPin
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodePermissionDenied, ErrBadToken)
		}
		if err != nil {
			return err
		}
		if jt.UsedAt.Valid {
			// The same node asking again (its first answer was lost).
			if jt.NodeID.Valid && (!relink.Valid || relink == jt.NodeID) {
				n, err := q.GetNode(ctx, jt.NodeID)
				if err == nil && bytes.Equal(n.PublicKey, key) && !n.DeletedAt.Valid {
					node = n
					return nil
				}
			}
			return connect.NewError(connect.CodePermissionDenied, ErrBadToken)
		}
		if !jt.ExpiresAt.Time.After(r.now()) {
			return connect.NewError(connect.CodePermissionDenied, ErrBadToken)
		}
		if relink.Valid {
			// Re-linking keeps the node's ID and hostname and takes the new
			// key, revoked or removed before or not. Only its own org's
			// tokens can do it.
			n, err := q.GetNode(ctx, relink)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && n.OrgID != jt.OrgID {
				return connect.NewError(connect.CodeNotFound, errors.New("no such node in the token's org: link it as a new node (raptor link)"))
			}
			if err != nil {
				return err
			}
			if node, err = q.RelinkNode(ctx, store.RelinkNodeParams{
				ID: n.ID, PublicKey: key, WingsVersion: truncate(req.GetWingsVersion(), 64), Facts: facts,
			}); err != nil {
				return err
			}
			return q.UseJoinToken(ctx, store.UseJoinTokenParams{ID: jt.ID, NodeID: node.ID})
		}
		for range 10 {
			short, err := newShortID()
			if err != nil {
				return err
			}
			taken, err := q.ShortIDTaken(ctx, short)
			if err != nil {
				return err
			}
			if taken {
				continue
			}
			node, err = q.CreateNode(ctx, store.CreateNodeParams{
				OrgID: jt.OrgID, Name: name, ShortID: short, PublicKey: key, Facts: facts,
				WingsVersion: truncate(req.GetWingsVersion(), 64),
			})
			if err != nil {
				return err
			}
			return q.UseJoinToken(ctx, store.UseJoinTokenParams{ID: jt.ID, NodeID: node.ID})
		}
		return errors.New("no free short ID")
	})
	if err != nil {
		return nil, err
	}
	if relink.Valid && r.KeyChanged != nil {
		r.KeyChanged(UUIDString(node.ID))
	}
	return &nodev1.EnrollResponse{
		NodeId: UUIDString(node.ID), ShortId: node.ShortID, PanelKey: r.PanelKey.Public().(ed25519.PublicKey),
		OwnerPin: pin,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NodeKey is the hub's check: an enrolled node's key, unless it was removed
// or its key revoked.
func (r *Registry) NodeKey(ctx context.Context, nodeID string) (ed25519.PublicKey, error) {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, nodelink.ErrUnknownNode
	}
	n, err := r.q().GetNode(ctx, pgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) || n.DeletedAt.Valid {
		return nil, nodelink.ErrUnknownNode
	}
	if err != nil {
		return nil, errors.New("the Panel couldn't look the node up; try again")
	}
	if n.KeyRevokedAt.Valid {
		return nil, errors.New("this node's key was revoked in the Panel: link it again (raptor relink)")
	}
	return ed25519.PublicKey(n.PublicKey), nil
}

// Connected records a node connecting (its versions), for the Panel to show.
func (r *Registry) Connected(ctx context.Context, h nodelink.Hello) error {
	id, err := uuid.Parse(h.NodeID)
	if err != nil {
		return err
	}
	return r.q().NodeConnected(ctx, store.NodeConnectedParams{
		ID: pgUUID(id), WingsVersion: truncate(h.Software, 64), ProtocolVersion: int32(min(h.Version, 1<<20)), //nolint:gosec // bounded
	})
}

// Disconnected records when a node was last connected.
func (r *Registry) Disconnected(ctx context.Context, nodeID string) error {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	return r.q().NodeSeen(ctx, pgUUID(id))
}

var _ nodev1connect.EnrollmentServiceHandler = (*Registry)(nil)
