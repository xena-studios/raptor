package orgs

import (
	"context"
	"encoding/json"
	"slices"

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
