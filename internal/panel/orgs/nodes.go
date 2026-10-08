package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// memberGrants is what a member may do in an org, by node and server; nil
// for admins and owners, who may do everything.
func memberGrants(ctx context.Context, q *store.Queries, org pgtype.UUID, sess *auth.Session, role string) (map[pgtype.UUID]map[string][]string, error) {
	if role != "member" {
		return nil, nil
	}
	rows, err := q.UserGrantsInOrg(ctx, store.UserGrantsInOrgParams{OrgID: org, UserID: sess.UserID})
	if err != nil {
		return nil, err
	}
	out := map[pgtype.UUID]map[string][]string{}
	for _, r := range rows {
		if out[r.NodeID] == nil {
			out[r.NodeID] = map[string][]string{}
		}
		out[r.NodeID][r.ServerID] = r.Permissions
	}
	return out, nil
}

// ListNodes implements OrgService.
func (s *Service) ListNodes(ctx context.Context, req *panelv1.ListNodesRequest) (*panelv1.ListNodesResponse, error) {
	out := &panelv1.ListNodesResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, role, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		grants, err := memberGrants(ctx, q, org, sess, role)
		if err != nil {
			return err
		}
		rows, err := q.OrgNodes(ctx, org)
		for _, r := range rows {
			if grants != nil && len(grants[r.ID]) == 0 {
				continue
			}
			n := &panelv1.Node{
				Id: idString(r.ID), Name: r.Name, ShortId: r.ShortID, WingsVersion: r.WingsVersion,
				Connected: r.Connected, CreatedAt: timestamppb.New(r.CreatedAt.Time),
				Arch: r.Arch, Cpus: r.Cpus, MemoryBytes: r.MemoryBytes,
				SftpEnabled: r.SftpEnabled, SftpPort: r.SftpPort, SftpHostKeyFingerprint: r.SftpHostKey,
			}
			if r.LastSeenAt.Valid {
				n.LastSeenAt = timestamppb.New(r.LastSeenAt.Time)
			}
			out.Nodes = append(out.Nodes, n)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListServers implements OrgService.
func (s *Service) ListServers(ctx context.Context, req *panelv1.ListServersRequest) (*panelv1.ListServersResponse, error) {
	out := &panelv1.ListServersResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, role, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		node, err := parseID(req.GetNodeId(), "node")
		if err != nil {
			return err
		}
		if owner, err := q.NodeOrg(ctx, node); err != nil || owner != org {
			return errNoServer
		}
		grants, err := memberGrants(ctx, q, org, sess, role)
		if err != nil {
			return err
		}
		rows, err := q.NodeServers(ctx, node)
		for _, r := range rows {
			perms := []string{"*"}
			if grants != nil {
				perms = grants[node][r.ServerID]
				if len(perms) == 0 {
					continue
				}
			}
			out.Servers = append(out.Servers, &panelv1.Server{
				Id: r.ServerID, Name: r.Name, State: r.State, EggName: r.EggName, Permissions: perms,
				Ports: ports(r.Allocations), InstallState: r.InstallState, InstallError: r.InstallError,
			})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type allocation struct {
	Port    int32 `json:"port"`
	Primary bool  `json:"primary"`
}

// ports lists a mirrored server's allocation ports, the primary first.
func ports(allocations []byte) []int32 {
	var as []allocation
	if json.Unmarshal(allocations, &as) != nil {
		return nil
	}
	slices.SortStableFunc(as, func(a, b allocation) int {
		switch {
		case a.Primary == b.Primary:
			return 0
		case a.Primary:
			return -1
		}
		return 1
	})
	out := make([]int32, len(as))
	for i, a := range as {
		out[i] = a.Port
	}
	return out
}

// GetServer implements OrgService.
func (s *Service) GetServer(ctx context.Context, req *panelv1.GetServerRequest) (*panelv1.GetServerResponse, error) {
	out := &panelv1.GetServerResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		_, node, perms, err := serverAccess(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId())
		if err != nil {
			return err
		}
		r, err := q.NodeServer(ctx, store.NodeServerParams{NodeID: node, ServerID: req.GetServerId()})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoServer
		}
		if err != nil {
			return err
		}
		out.Server = &panelv1.Server{
			Id: r.ServerID, Name: r.Name, State: r.State, EggName: r.EggName, Permissions: perms,
			Ports: ports(r.Allocations), InstallState: r.InstallState, InstallError: r.InstallError,
		}
		if slices.Contains(perms, "*") || slices.Contains(perms, "startup") {
			out.ConfigJson = string(r.Config)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// serverAccess checks the signed-in user can see a server of the org's,
// and returns what they may do on it ("*" for admins and owners).
func serverAccess(ctx context.Context, q *store.Queries, sess *auth.Session, orgID, nodeID, serverID string) (org, node pgtype.UUID, perms []string, err error) {
	org, role, err := member(ctx, q, sess, orgID, "member")
	if err != nil {
		return org, node, nil, err
	}
	if node, err = parseID(nodeID, "node"); err != nil {
		return org, node, nil, err
	}
	if owner, err := q.NodeOrg(ctx, node); err != nil || owner != org {
		return org, node, nil, errNoServer
	}
	if role != "member" {
		return org, node, []string{"*"}, nil
	}
	grants, err := memberGrants(ctx, q, org, sess, role)
	if err != nil {
		return org, node, nil, err
	}
	if perms = grants[node][serverID]; len(perms) == 0 {
		return org, node, nil, errNoServer
	}
	return org, node, perms, nil
}

// nodeOf checks the signed-in user is an admin or owner of the org the
// node is in.
func (s *Service) nodeOf(ctx context.Context, orgID, nodeID string) (*auth.Session, pgtype.UUID, pgtype.UUID, error) {
	var sess *auth.Session
	var org, node pgtype.UUID
	err := s.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		var err error
		if org, _, err = member(ctx, q, se, orgID, "admin"); err != nil {
			return err
		}
		if node, err = parseID(nodeID, "node"); err != nil {
			return err
		}
		if owner, err := q.NodeOrg(ctx, node); err != nil || owner != org {
			return connect.NewError(connect.CodeNotFound, errors.New("no such node"))
		}
		return nil
	})
	return sess, org, node, err
}

// RenameNode implements OrgService.
func (s *Service) RenameNode(ctx context.Context, req *panelv1.RenameNodeRequest) (*panelv1.RenameNodeResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" || utf8.RuneCountInString(name) > maxOrgName || !utf8.ValidString(name) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a node's name is 1 to %d characters", maxOrgName))
	}
	sess, org, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if _, err := store.New(s.DB).RenameNode(ctx, store.RenameNodeParams{ID: node, Name: name}); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, nil, sess, org, "node.rename", idString(node), nil, map[string]any{"name": name})
	return &panelv1.RenameNodeResponse{}, nil
}

// RemoveNode implements OrgService.
func (s *Service) RemoveNode(ctx context.Context, req *panelv1.RemoveNodeRequest) (*panelv1.RemoveNodeResponse, error) {
	if s.Registry == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("nodes aren't managed by this Panel"))
	}
	sess, org, node, err := s.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RequireReauth(sess); err != nil {
		return nil, err
	}
	removed, err := s.Registry.Remove(ctx, idString(node))
	if err != nil {
		return nil, err
	}
	if removed {
		_ = s.audit(ctx, nil, sess, org, "node.remove", idString(node), nil, nil)
		if s.NodeRemoved != nil {
			s.NodeRemoved(context.WithoutCancel(ctx), idString(node))
		}
	}
	return &panelv1.RemoveNodeResponse{}, nil
}
