// Package backupkeys keeps copies of nodes' backup keys (docs/PANEL.md#backup-keys):
// each node encrypts its backups with its own key, so a node that dies
// takes the only copy with it unless one is kept elsewhere. By default the
// Panel keeps one, sealed with PANEL_DATA_KEY; an owner can take it back
// and keep the only copy themselves.
package backupkeys

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// UserID is who the keeper's commands are from.
const UserID = "panel:backup-keys"

// lockID is the advisory lock a tick holds.
const lockID = 0x7261_7074_6b65_7973 // "raptkeys"

// Sender delivers a command to a node (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Keeper fetches, seals, and forgets nodes' backup keys.
type Keeper struct {
	DB       *pgxpool.Pool
	Sender   Sender
	PanelKey ed25519.PrivateKey
	// DataKey seals the keys (32 bytes, PANEL_DATA_KEY); nil: no copies
	// are kept.
	DataKey []byte
	Log     *slog.Logger
}

// ErrNoDataKey means the Panel has no data key to seal backup keys with.
var ErrNoDataKey = errors.New("this Panel can't keep backup keys (PANEL_DATA_KEY isn't set)")

// Enabled reports whether this Panel keeps copies.
func (k *Keeper) Enabled() bool { return k != nil && len(k.DataKey) == 32 }

func (k *Keeper) log() *slog.Logger {
	if k.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return k.Log
}

func (k *Keeper) aead() (cipher.AEAD, error) {
	if !k.Enabled() {
		return nil, ErrNoDataKey
	}
	block, err := aes.NewCipher(k.DataKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aad binds a sealed key to its node, so one copied to another row doesn't
// open.
func aad(node uuid.UUID) []byte { return append([]byte("raptor backup key v1\x00"), node[:]...) }

func (k *Keeper) seal(node uuid.UUID, key string) ([]byte, error) {
	a, err := k.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(append([]byte{1}, nonce...), nonce, []byte(key), aad(node)), nil
}

func (k *Keeper) open(node uuid.UUID, sealed []byte) (string, error) {
	a, err := k.aead()
	if err != nil {
		return "", err
	}
	n := 1 + a.NonceSize()
	if len(sealed) < n || sealed[0] != 1 {
		return "", errors.New("unknown sealed backup key format")
	}
	pt, err := a.Open(nil, sealed[1:n], sealed[n:], aad(node))
	if err != nil {
		return "", fmt.Errorf("opening a backup key: %w", err)
	}
	return string(pt), nil
}

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// Sync asks a node for its key and keeps a copy, or forgets the copy if
// its owner keeps the key. It returns the node's mode.
func (k *Keeper) Sync(ctx context.Context, org, node uuid.UUID) (string, error) {
	if !k.Enabled() {
		return "", ErrNoDataKey
	}
	env, err := nodecmd.New(k.PanelKey, node.String(), UserID, "backup.key", "", nil, time.Minute)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := k.Sender.Execute(cctx, node.String(), raw)
	if err != nil {
		return "", err
	}
	if res.GetError() != "" {
		return "", errors.New(res.GetError())
	}
	var got struct {
		Mode        string `json:"mode"`
		Key         string `json:"key"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(res.GetResult(), &got); err != nil {
		return "", err
	}
	q := store.New(k.DB)
	if got.Mode != "panel" || got.Key == "" {
		return got.Mode, q.DeleteBackupKey(ctx, pgID(node))
	}
	sealed, err := k.seal(node, got.Key)
	if err != nil {
		return "", err
	}
	return got.Mode, q.UpsertBackupKey(ctx, store.UpsertBackupKeyParams{NodeID: pgID(node), OrgID: pgID(org), Sealed: sealed, Fingerprint: got.Fingerprint})
}

// Connected asks a node that just connected for its key, if the Panel has
// no copy yet: a new node's copy is kept within seconds of it linking.
func (k *Keeper) Connected(ctx context.Context, nodeID string) {
	if !k.Enabled() {
		return
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return
	}
	if st, err := k.Info(ctx, id); err != nil || st != nil {
		return
	}
	org, err := store.New(k.DB).NodeOrg(ctx, pgID(id))
	if err != nil || !org.Valid {
		return
	}
	time.Sleep(2 * time.Second) // until the connection is routable
	if _, err := k.Sync(ctx, uuid.UUID(org.Bytes), id); err != nil {
		k.log().Debug("asking a node for its backup key", "node", nodeID, "err", err)
	}
}

// Forget drops the Panel's copy of a node's key.
func (k *Keeper) Forget(ctx context.Context, node uuid.UUID) error {
	return store.New(k.DB).DeleteBackupKey(ctx, pgID(node))
}

// Stored is what the Panel has of a node's key: whether it has a copy, and
// when it was stored.
type Stored struct {
	Fingerprint string
	At          time.Time
}

// Info returns the stored copy's details, or nil.
func (k *Keeper) Info(ctx context.Context, node uuid.UUID) (*Stored, error) {
	r, err := store.New(k.DB).GetBackupKey(ctx, pgID(node))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Stored{Fingerprint: r.Fingerprint, At: r.StoredAt.Time}, nil
}

// Key opens a node's stored key, for recovering its backups elsewhere. The
// caller checks the user may (an owner of the node's org).
func (k *Keeper) Key(ctx context.Context, org, node uuid.UUID) (string, error) {
	r, err := store.New(k.DB).GetBackupKey(ctx, pgID(node))
	if err != nil {
		return "", err
	}
	if r.OrgID != pgID(org) {
		return "", pgx.ErrNoRows
	}
	return k.open(node, r.Sealed)
}

// Run asks nodes the Panel has no copy for, every interval until ctx ends.
func (k *Keeper) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := k.Tick(ctx); err != nil && ctx.Err() == nil {
			k.log().Warn("backup key tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick asks every linked node without a copy for its key. Offline nodes
// are asked again next time. One Panel instance at a time.
func (k *Keeper) Tick(ctx context.Context) error {
	if !k.Enabled() {
		return nil
	}
	conn, err := k.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()
	rows, err := store.New(k.DB).NodesWithoutBackupKey(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := k.Sync(ctx, uuid.UUID(r.OrgID.Bytes), uuid.UUID(r.ID.Bytes)); err != nil && ctx.Err() == nil {
			k.log().Debug("asking a node for its backup key", "node", uuid.UUID(r.ID.Bytes), "err", err)
		}
	}
	return nil
}
