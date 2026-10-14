package orgs

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/subdomains"
)

// Subdomains implements SubdomainService (docs/PANEL.md#subdomains).
type Subdomains struct {
	Orgs *Service
	// Domain is the node domain names are on; empty turns subdomains off
	// (until raptornodes.net is on the Public Suffix List, DECISIONS #85).
	Domain string
	// Sync is called after a change, to write records without waiting
	// for the next sweep (nil: the sweep does).
	Sync func()
}

var _ panelv1connect.SubdomainServiceHandler = (*Subdomains)(nil)

// Limits.
const (
	// subdomainsPerOrg is how many an org may have.
	subdomainsPerOrg = 20
	// subdomainChangesPerHour bounds creating and deleting, per user.
	subdomainChangesPerHour  = 20
	subdomainChecksPerMinute = 60
)

var errSubdomainsOff = connect.NewError(connect.CodeFailedPrecondition, errors.New("this Panel doesn't offer subdomains yet"))

func (s *Subdomains) sync() {
	if s.Sync != nil {
		go s.Sync()
	}
}

// ListSubdomains implements SubdomainService.
func (s *Subdomains) ListSubdomains(ctx context.Context, req *panelv1.ListSubdomainsRequest) (*panelv1.ListSubdomainsResponse, error) {
	out := &panelv1.ListSubdomainsResponse{Domain: s.Domain, Limit: subdomainsPerOrg}
	err := s.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		rows, err := q.OrgSubdomains(ctx, org)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Subdomains = append(out.Subdomains, &panelv1.Subdomain{
				Id: idString(r.ID), NodeId: idString(r.NodeID), ServerId: r.ServerID, Name: r.Name,
				CreatedAt: timestamppb.New(r.CreatedAt.Time), Live: r.DnsTarget != "" && r.DnsError == "", Error: r.DnsError,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// availability says why an org can't have a name, or "" if it can.
func availability(ctx context.Context, q *store.Queries, org pgtype.UUID, name string) (string, error) {
	if err := subdomains.CheckName(name); err != nil {
		return err.Error(), nil //nolint:nilerr // a bad name is an answer, not a failure
	}
	banned, err := q.SubdomainBanned(ctx, name)
	if err != nil {
		return "", err
	}
	if banned {
		return subdomains.ErrReserved.Error(), nil
	}
	h, err := q.SubdomainHolder(ctx, name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	case h.OrgID == org && h.ReleasedAt.Valid:
		return "", nil // the org's own, released lately
	case h.OrgID == org:
		return "your org already uses that name", nil
	}
	return subdomains.ErrTaken.Error(), nil
}

// CheckSubdomain implements SubdomainService.
func (s *Subdomains) CheckSubdomain(ctx context.Context, req *panelv1.CheckSubdomainRequest) (*panelv1.CheckSubdomainResponse, error) {
	if s.Domain == "" {
		return nil, errSubdomainsOff
	}
	var org pgtype.UUID
	err := s.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		var err error
		if org, _, err = member(ctx, q, sess, req.GetOrgId(), "admin"); err != nil {
			return err
		}
		return s.Orgs.Auth.RateLimit(ctx, "subdomaincheck:user:"+idString(sess.UserID), subdomainChecksPerMinute, time.Minute)
	})
	if err != nil {
		return nil, err
	}
	// Other orgs' names are only visible to the Panel's own connection.
	reason, err := availability(ctx, store.New(s.Orgs.DB), org, strings.ToLower(strings.TrimSpace(req.GetName())))
	if err != nil {
		return nil, err
	}
	return &panelv1.CheckSubdomainResponse{Available: reason == "", Reason: reason}, nil
}

// CreateSubdomain implements SubdomainService.
func (s *Subdomains) CreateSubdomain(ctx context.Context, req *panelv1.CreateSubdomainRequest) (*panelv1.CreateSubdomainResponse, error) {
	if s.Domain == "" {
		return nil, errSubdomainsOff
	}
	name := strings.ToLower(strings.TrimSpace(req.GetName()))
	sess, org, node, err := s.Orgs.nodeOf(ctx, req.GetOrgId(), req.GetNodeId())
	if err != nil {
		return nil, err
	}
	if err := s.Orgs.Auth.RateLimit(ctx, "subdomain:user:"+idString(sess.UserID), subdomainChangesPerHour, time.Hour); err != nil {
		return nil, err
	}
	q := store.New(s.Orgs.DB)
	exists, err := q.MirrorServerExists(ctx, store.MirrorServerExistsParams{NodeID: node, ServerID: req.GetServerId()})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such server"))
	}
	reason, err := availability(ctx, q, org, name)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		code := connect.CodeAlreadyExists
		if subdomains.CheckName(name) != nil {
			code = connect.CodeInvalidArgument
		}
		return nil, connect.NewError(code, errors.New(reason))
	}
	n, err := q.CountOrgSubdomains(ctx, org)
	if err != nil {
		return nil, err
	}
	if n >= subdomainsPerOrg {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("an org has at most 20 subdomains"))
	}
	id, err := q.ClaimSubdomain(ctx, store.ClaimSubdomainParams{OrgID: org, NodeID: node, ServerID: req.GetServerId(), Name: name, CreatedBy: sess.UserID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, connect.NewError(connect.CodeAlreadyExists, subdomains.ErrTaken)
	case isUniqueViolation(err):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("this server already has a subdomain"))
	case err != nil:
		return nil, err
	}
	_ = s.audit(ctx, sess, org, "subdomain.create", name, map[string]any{"node": idString(node), "server": req.GetServerId()})
	s.sync()
	return &panelv1.CreateSubdomainResponse{Subdomain: &panelv1.Subdomain{
		Id: idString(id), NodeId: idString(node), ServerId: req.GetServerId(), Name: name, CreatedAt: timestamppb.Now(),
	}}, nil
}

// DeleteSubdomain implements SubdomainService.
func (s *Subdomains) DeleteSubdomain(ctx context.Context, req *panelv1.DeleteSubdomainRequest) (*panelv1.DeleteSubdomainResponse, error) {
	var sess *auth.Session
	var org pgtype.UUID
	err := s.Orgs.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		var err error
		org, _, err = member(ctx, q, se, req.GetOrgId(), "admin")
		return err
	})
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId(), "subdomain")
	if err != nil {
		return nil, err
	}
	if _, err := store.New(s.Orgs.DB).ReleaseSubdomain(ctx, store.ReleaseSubdomainParams{ID: id, OrgID: org}); errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such subdomain"))
	} else if err != nil {
		return nil, err
	}
	_ = s.audit(ctx, sess, org, "subdomain.delete", req.GetId(), nil)
	s.sync()
	return &panelv1.DeleteSubdomainResponse{}, nil
}

// isUniqueViolation: the server already has a subdomain
// (subdomains_server).
func isUniqueViolation(err error) bool {
	pgErr := (*pgconn.PgError)(nil)
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Subdomains) audit(ctx context.Context, sess *auth.Session, org pgtype.UUID, action, target string, meta map[string]any) error {
	return s.Orgs.audit(ctx, nil, sess, org, action, target, nil, meta)
}
