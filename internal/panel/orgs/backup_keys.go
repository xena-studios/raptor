package orgs

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
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

// ListRecoverySources implements OrgService.
func (s *Service) ListRecoverySources(ctx context.Context, req *panelv1.ListRecoverySourcesRequest) (*panelv1.ListRecoverySourcesResponse, error) {
	var org pgtype.UUID
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		var err error
		org, _, err = member(ctx, q, sess, req.GetOrgId(), "owner")
		return err
	})
	if err != nil {
		return nil, err
	}
	// The key and storage tables are the Panel's own: read with its pool.
	rows, err := store.New(s.DB).RecoverySources(ctx, org)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListRecoverySourcesResponse{}
	for _, r := range rows {
		out.Sources = append(out.Sources, &panelv1.RecoverySource{
			NodeId: idString(r.ID), Name: r.Name, Removed: r.DeletedAt.Valid, KeyKept: r.KeyKept, HasStorage: r.HasStorage,
		})
	}
	return out, nil
}

// PrepareRecovery implements OrgService.
func (s *Service) PrepareRecovery(ctx context.Context, req *panelv1.PrepareRecoveryRequest) (*panelv1.PrepareRecoveryResponse, error) {
	var sess *auth.Session
	var org, from pgtype.UUID
	var name string
	err := s.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		var err error
		sess = se
		if org, _, err = member(ctx, q, se, req.GetOrgId(), "owner"); err != nil {
			return err
		}
		if from, err = parseID(req.GetFromNodeId(), "node"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Removed nodes too, so read with the Panel's pool; it must be the org's.
	var nodeOrg pgtype.UUID
	if err := s.DB.QueryRow(ctx, "SELECT org_id, name FROM nodes WHERE id = $1", from).Scan(&nodeOrg, &name); err != nil || nodeOrg != org {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such node"))
	}
	out := &panelv1.PrepareRecoveryResponse{}
	if s.Keys.Enabled() {
		key, err := s.Keys.Key(ctx, uuid.UUID(org.Bytes), uuid.UUID(from.Bytes))
		switch {
		case err == nil:
			out.Key = key
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}
	if req.GetStorage() {
		dest, err := s.Storage.ReadOnlyDestination(ctx, uuid.UUID(org.Bytes), uuid.UUID(from.Bytes), name+"'s Raptor Backup Storage")
		if err != nil {
			s.logger().Error("making a recovery key failed", "node", idString(from), "err", err)
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("couldn't reach Raptor Backup Storage just now; try again in a minute"))
		}
		b, err := json.Marshal(dest)
		if err != nil {
			return nil, err
		}
		out.DestinationJson = string(b)
	}
	_ = s.audit(ctx, nil, sess, org, "backup.recover.prepare", idString(from), nil, map[string]any{"storage": req.GetStorage(), "key": out.Key != ""})
	return out, nil
}
