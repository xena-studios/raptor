package orgs

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/storage"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/hosted"
)

// GetBackupStorage implements OrgService.
func (s *Service) GetBackupStorage(ctx context.Context, req *panelv1.GetBackupStorageRequest) (*panelv1.GetBackupStorageResponse, error) {
	out := &panelv1.GetBackupStorageResponse{
		Available: s.Storage.Available(), CentsPerTb: hosted.CentsPerTB, SoftCapBytes: hosted.SoftCapBytes,
	}
	var nodes int
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		n, err := q.CountOrgNodes(ctx, org)
		if err != nil {
			return err
		}
		nodes = int(n)
		// The storage tables are the Panel's own: read with its pool.
		sq := store.New(s.DB)
		on, err := sq.OrgBackupStorage(ctx, org)
		if err != nil {
			return err
		}
		for _, r := range on {
			out.NodeIds = append(out.NodeIds, idString(r.NodeID))
		}
		u, err := sq.LatestBackupStorageUsage(ctx, org)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		out.UsedBytes, out.MeasuredAt = u.Bytes, ts(u.MeasuredAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	out.IncludedBytes = int64(nodes) * hosted.IncludedBytesPerNode
	out.EstimateCents = storage.Estimate(out.UsedBytes, nodes)
	return out, nil
}

// EnableBackupStorage implements OrgService.
func (s *Service) EnableBackupStorage(ctx context.Context, req *panelv1.EnableBackupStorageRequest) (*panelv1.EnableBackupStorageResponse, error) {
	if !s.Storage.Available() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, storage.ErrOff)
	}
	sess, org, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if err := s.Storage.Enable(ctx, uuid.UUID(org.Bytes), uuid.UUID(node.Bytes), sess.UserID); err != nil {
		if errors.Is(err, storage.ErrOffline) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		s.logger().Error("turning on backup storage failed", "node", idString(node), "err", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("couldn't turn on Raptor Backup Storage just now; try again in a minute"))
	}
	_ = s.audit(ctx, nil, sess, org, "backup_storage.enable", idString(node), nil, nil)
	return &panelv1.EnableBackupStorageResponse{}, nil
}

// DisableBackupStorage implements OrgService.
func (s *Service) DisableBackupStorage(ctx context.Context, req *panelv1.DisableBackupStorageRequest) (*panelv1.DisableBackupStorageResponse, error) {
	sess, org, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if s.Storage == nil {
		return &panelv1.DisableBackupStorageResponse{}, nil
	}
	if err := s.Storage.Disable(ctx, uuid.UUID(node.Bytes)); err != nil {
		s.logger().Error("turning off backup storage failed", "node", idString(node), "err", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("couldn't turn off Raptor Backup Storage just now; try again in a minute"))
	}
	_ = s.audit(ctx, nil, sess, org, "backup_storage.disable", idString(node), nil, nil)
	return &panelv1.DisableBackupStorageResponse{}, nil
}

func (s *Service) logger() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}
