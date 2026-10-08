package orgs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// sftpAccess is how to connect with a temporary password.
func sftpAccess(n store.Node, serverID, username string, expires pgtype.Timestamptz) *panelv1.SFTPAccess {
	short := serverID
	if len(short) > 8 {
		short = short[len(short)-8:]
	}
	return &panelv1.SFTPAccess{
		Host: "n-" + n.ShortID + ".raptornodes.net", Port: n.SftpPort, Username: username + "." + short,
		ExpiresAt: timestamppb.New(expires.Time), HostKeyFingerprint: n.SftpHostKey,
	}
}

// GetSFTPAccess implements OrgService.
func (s *Service) GetSFTPAccess(ctx context.Context, req *panelv1.GetSFTPAccessRequest) (*panelv1.GetSFTPAccessResponse, error) {
	out := &panelv1.GetSFTPAccessResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		node, err := serverWith(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId(), "sftp")
		if err != nil {
			return err
		}
		row, err := s.q().GetSFTPPassword(ctx, store.GetSFTPPasswordParams{UserID: sess.UserID, NodeID: node, ServerID: req.GetServerId()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !row.ExpiresAt.Time.After(s.now()) {
			return nil
		}
		n, err := s.q().GetNode(ctx, node)
		if err != nil {
			return err
		}
		out.Access = sftpAccess(n, req.GetServerId(), row.Username, row.ExpiresAt)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateSFTPAccess implements OrgService.
func (s *Service) CreateSFTPAccess(ctx context.Context, req *panelv1.CreateSFTPAccessRequest) (*panelv1.CreateSFTPAccessResponse, error) {
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	if ttl == 0 {
		ttl = auth.SFTPPasswordDefault
	}
	if ttl < auth.SFTPPasswordMin || ttl > auth.SFTPPasswordMax {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a password lasts from %s to %d days", auth.SFTPPasswordMin, int(auth.SFTPPasswordMax.Hours()/24)))
	}
	out := &panelv1.CreateSFTPAccessResponse{}
	var sess *auth.Session
	err := s.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		node, err := serverWith(ctx, q, se, req.GetOrgId(), req.GetNodeId(), req.GetServerId(), "sftp")
		if err != nil {
			return err
		}
		username, password, secret := auth.NewSFTPPassword()
		// The table isn't visible to requests' role: written as the Panel,
		// for the user the checks above are about.
		row, err := s.q().SetSFTPPassword(ctx, store.SetSFTPPasswordParams{
			UserID: se.UserID, NodeID: node, ServerID: req.GetServerId(), Username: username, SecretHash: secret,
			ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(ttl), Valid: true},
		})
		if err != nil {
			return err
		}
		n, err := s.q().GetNode(ctx, node)
		if err != nil {
			return err
		}
		out.Access, out.Password = sftpAccess(n, req.GetServerId(), row.Username, row.ExpiresAt), password
		return nil
	})
	if err != nil {
		return nil, err
	}
	org, _ := parseID(req.GetOrgId(), "org")
	_ = s.Auth.Audit(ctx, nil, auth.Event{Org: org, Actor: sess.UserID, Action: "sftp.password", Target: req.GetServerId(), Meta: map[string]any{"node": req.GetNodeId(), "expires_at": out.GetAccess().GetExpiresAt().AsTime()}})
	return out, nil
}

// RevokeSFTPAccess implements OrgService.
func (s *Service) RevokeSFTPAccess(ctx context.Context, req *panelv1.RevokeSFTPAccessRequest) (*panelv1.RevokeSFTPAccessResponse, error) {
	out := &panelv1.RevokeSFTPAccessResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		// Anyone may give up their own password, even after losing access.
		_, node, _, err := serverAccess(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId())
		if err != nil && !errors.Is(err, errNoServer) {
			return err
		}
		if !node.Valid {
			return errNoServer
		}
		row, err := s.q().DeleteSFTPPassword(ctx, store.DeleteSFTPPasswordParams{UserID: sess.UserID, NodeID: node, ServerID: req.GetServerId()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		out.Username = row.Username
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// q is the Panel's own queries, outside row-level security: for tables
// requests' role can't see, after the checks that apply.
func (s *Service) q() *store.Queries { return store.New(s.DB) }
