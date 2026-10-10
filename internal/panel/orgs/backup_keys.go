package orgs

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
)

// GetBackupKey implements OrgService.
func (s *Service) GetBackupKey(ctx context.Context, req *panelv1.GetBackupKeyRequest) (*panelv1.GetBackupKeyResponse, error) {
	_, _, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetBackupKeyResponse{Available: s.Keys.Enabled()}
	if !out.Available {
		return out, nil
	}
	st, err := s.Keys.Info(ctx, uuid.UUID(node.Bytes))
	if err != nil {
		return nil, err
	}
	if st != nil {
		out.Kept, out.Fingerprint, out.StoredAt = true, st.Fingerprint, timestamppb.New(st.At)
	}
	return out, nil
}

// SyncBackupKey implements OrgService.
func (s *Service) SyncBackupKey(ctx context.Context, req *panelv1.SyncBackupKeyRequest) (*panelv1.SyncBackupKeyResponse, error) {
	_, org, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if !s.Keys.Enabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this Panel doesn't keep backup keys"))
	}
	mode, err := s.Keys.Sync(ctx, uuid.UUID(org.Bytes), uuid.UUID(node.Bytes))
	if err != nil {
		s.logger().Warn("syncing a backup key failed", "node", idString(node), "err", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("couldn't reach the node; it's checked again within 15 minutes"))
	}
	return &panelv1.SyncBackupKeyResponse{Mode: mode}, nil
}
