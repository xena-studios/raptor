package orgs

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/panel/store"
)

var errNoServer = connect.NewError(connect.CodeNotFound, errors.New("no such server in this org"))

// orgServer checks node is the org's and has server (as the mirror knows it).
func orgServer(ctx context.Context, q *store.Queries, org pgtype.UUID, nodeID, server string) (pgtype.UUID, error) {
	node, err := parseID(nodeID, "node")
	if err != nil {
		return node, err
	}
	owner, err := q.NodeOrg(ctx, node)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != org) {
		return node, errNoServer
	}
	if err != nil {
		return node, err
	}
	ok, err := q.MirroredServerExists(ctx, store.MirroredServerExistsParams{NodeID: node, ServerID: server})
	if err == nil && !ok {
		return node, errNoServer
	}
	return node, err
}

// SetServerAccess implements OrgService.
func (s *Service) SetServerAccess(ctx context.Context, req *panelv1.SetServerAccessRequest) (*panelv1.SetServerAccessResponse, error) {
	user, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	list := slices.Clone(req.GetPermissions())
	slices.Sort(list)
	list = slices.Compact(list)
	for _, p := range list {
		if !perms.Valid(p) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown permission "+p))
		}
	}
	err = s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		node, err := orgServer(ctx, q, org, req.GetNodeId(), req.GetServerId())
		if err != nil {
			return err
		}
		if len(list) == 0 {
			if _, err := q.DeleteServerGrant(ctx, store.DeleteServerGrantParams{NodeID: node, ServerID: req.GetServerId(), UserID: user}); err != nil {
				return err
			}
			return s.audit(ctx, q, sess, org, "access.remove", req.GetServerId(), &user, map[string]any{"node": req.GetNodeId()})
		}
		err = q.SetServerGrant(ctx, store.SetServerGrantParams{
			OrgID: org, UserID: user, NodeID: node, ServerID: req.GetServerId(), Permissions: list, GrantedBy: sess.UserID,
		})
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return connect.NewError(connect.CodeNotFound, errors.New("they're not in this org"))
		}
		if err != nil {
			return err
		}
		return s.audit(ctx, q, sess, org, "access.set", req.GetServerId(), &user, map[string]any{"node": req.GetNodeId(), "permissions": list})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.SetServerAccessResponse{}, nil
}

// ListServerAccess implements OrgService.
func (s *Service) ListServerAccess(ctx context.Context, req *panelv1.ListServerAccessRequest) (*panelv1.ListServerAccessResponse, error) {
	out := &panelv1.ListServerAccessResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		node, err := orgServer(ctx, q, org, req.GetNodeId(), req.GetServerId())
		if err != nil {
			return err
		}
		rows, err := q.ServerGrants(ctx, store.ServerGrantsParams{NodeID: node, ServerID: req.GetServerId()})
		for _, r := range rows {
			out.Access = append(out.Access, &panelv1.ServerAccess{UserId: idString(r.UserID), Email: r.Email, Permissions: r.Permissions})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
