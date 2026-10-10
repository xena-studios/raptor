package storage

import (
	"context"
	"crypto/ed25519"
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
	"github.com/xena-studios/raptor/internal/shared/hosted"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// UserID is who Raptor Backup Storage's commands are from.
const UserID = "panel:backup-storage"

// PurgeAfter is how long a node's data is kept after storage is turned off
// or the node removed (docs/PANEL.md#billing-polar).
const PurgeAfter = 30 * 24 * time.Hour

// lockID is the advisory lock a tick holds, so one Panel instance at a
// time measures and purges.
const lockID = 0x7261_7074_7374_6f72 // "raptstor"

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Service turns Raptor Backup Storage on and off for nodes, and measures
// and cleans up behind them.
type Service struct {
	DB       *pgxpool.Pool
	B2       *B2
	BucketID string
	// Endpoint is the bucket's S3 endpoint (s3.<region>.backblazeb2.com).
	Endpoint string
	Region   string
	Sender   Sender
	PanelKey ed25519.PrivateKey
	Log      *slog.Logger
	Now      func() time.Time
}

// Errors, worded for people.
var (
	ErrOff     = errors.New("this Panel doesn't offer Raptor Backup Storage")
	ErrOffline = errors.New("the node is offline; turn Raptor Backup Storage on when it's back")
)

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

// Available reports whether this Panel offers it (its B2 settings are set).
func (s *Service) Available() bool {
	return s != nil && s.B2 != nil && s.BucketID != "" && hosted.Endpoint(s.Endpoint)
}

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// Enable turns it on for a node: a key for the node's folder, sent to the
// node as its "raptor" destination. Turning it on again replaces the key.
func (s *Service) Enable(ctx context.Context, org, node uuid.UUID, by pgtype.UUID) error {
	if !s.Available() {
		return ErrOff
	}
	prefix := hosted.Prefix(org.String(), node.String())
	key, err := s.B2.CreateKey(ctx, "raptor-node-"+node.String(), s.BucketID, prefix)
	if err != nil {
		return fmt.Errorf("make a key: %w", err)
	}
	dest := map[string]any{
		"name": "Raptor Backup Storage", "type": "raptor",
		"raptor": map[string]string{
			"endpoint": s.Endpoint, "region": s.Region, "bucket": hosted.Bucket, "prefix": prefix,
			"access_key": key.ID, "secret_key": key.Secret,
		},
	}
	if err := s.send(ctx, node.String(), "backup.destination.save", dest); err != nil {
		_ = s.B2.DeleteKey(context.WithoutCancel(ctx), key.ID)
		return err
	}
	var replaced string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		old, err := q.DisableBackupStorage(ctx, pgID(node))
		switch {
		case err == nil:
			// Replaced, not turned off: the data stays (the new key is for
			// the same folder), so there's nothing to delete later.
			replaced = old.KeyID
			if err := q.SetBackupStoragePurged(ctx, old.ID); err != nil {
				return err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		_, err = q.InsertBackupStorage(ctx, store.InsertBackupStorageParams{OrgID: pgID(org), NodeID: pgID(node), KeyID: key.ID, EnabledBy: by})
		return err
	})
	if err != nil {
		return err
	}
	if replaced != "" {
		if err := s.B2.DeleteKey(ctx, replaced); err != nil {
			s.log().Warn("deleting a replaced backup storage key failed", "node", node, "err", err)
		}
	}
	return nil
}

// Disable turns it off for a node (after the node removed its destination,
// or the node itself was removed): the key is deleted now, the data in 30
// days. A node without it is fine.
func (s *Service) Disable(ctx context.Context, node uuid.UUID) error {
	row, err := store.New(s.DB).DisableBackupStorage(ctx, pgID(node))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.B2 == nil {
		return nil
	}
	return s.B2.DeleteKey(ctx, row.KeyID)
}

func (s *Service) send(ctx context.Context, nodeID, action string, params any) error {
	env, err := nodecmd.New(s.PanelKey, nodeID, UserID, action, "", params, time.Minute)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.Sender.Execute(cctx, nodeID, raw)
	if err != nil {
		return ErrOffline
	}
	if res.GetError() != "" {
		return errors.New(res.GetError())
	}
	return nil
}

// Run measures and purges every interval until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("backup storage tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick measures each org not yet measured today, and deletes the data of
// nodes turned off over 30 days ago. One Panel instance at a time.
func (s *Service) Tick(ctx context.Context) error {
	if !s.Available() {
		return nil
	}
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()

	q := store.New(s.DB)
	now := s.now().UTC()
	purge, err := q.BackupStorageToPurge(ctx, pgtype.Timestamptz{Time: now.Add(-PurgeAfter), Valid: true})
	if err != nil {
		return err
	}
	for _, r := range purge {
		prefix := hosted.Prefix(uuid.UUID(r.OrgID.Bytes).String(), uuid.UUID(r.NodeID.Bytes).String())
		n, err := s.B2.DeletePrefix(ctx, s.BucketID, prefix)
		if err != nil {
			s.log().Warn("deleting a node's backup storage failed", "node", uuid.UUID(r.NodeID.Bytes), "err", err)
			continue
		}
		s.log().Info("deleted a node's backup storage", "node", uuid.UUID(r.NodeID.Bytes), "files", n)
		if err := q.SetBackupStoragePurged(ctx, r.ID); err != nil {
			return err
		}
	}
	day := pgtype.Date{Time: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	orgs, err := q.BackupStorageOrgsToMeasure(ctx, day)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		bytes, err := s.B2.Usage(ctx, s.BucketID, hosted.OrgPrefix(uuid.UUID(org.Bytes).String()))
		if err != nil {
			s.log().Warn("measuring backup storage failed", "org", uuid.UUID(org.Bytes), "err", err)
			continue
		}
		if err := q.InsertBackupStorageUsage(ctx, store.InsertBackupStorageUsageParams{OrgID: org, Day: day, Bytes: bytes}); err != nil {
			return err
		}
		if bytes > hosted.SoftCapBytes {
			s.log().Warn("an org is over the backup storage soft cap", "org", uuid.UUID(org.Bytes), "bytes", bytes)
		}
	}
	return nil
}

// Estimate is what an org's storage would cost a month, in cents: what's
// past the included amount, at the TB price.
func Estimate(used int64, nodes int) int64 {
	over := used - int64(nodes)*hosted.IncludedBytesPerNode
	if over <= 0 {
		return 0
	}
	return (over*hosted.CentsPerTB + 1e12 - 1) / 1e12
}
