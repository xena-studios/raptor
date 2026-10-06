// Package orgs serves OrgService: orgs, members, roles, and invitations
// (docs/PANEL.md#permissions).
package orgs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Limits.
const (
	InvitationTTL  = 7 * 24 * time.Hour
	MaxOwnedOrgs   = 10
	MaxPending     = 100
	maxOrgName     = 64
	invitesPerDay  = 50
	joinTokensHour = 20
)

// Service is the OrgService handler.
type Service struct {
	DB       *pgxpool.Pool
	Auth     *auth.Service
	Registry *nodes.Registry
	Now      func() time.Time
}

var _ panelv1connect.OrgServiceHandler = (*Service)(nil)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var (
	errNotFound = connect.NewError(connect.CodeNotFound, errors.New("no such org"))
	errDenied   = connect.NewError(connect.CodePermissionDenied, errors.New("your role in this org can't do that"))
	errOwner    = connect.NewError(connect.CodeFailedPrecondition, errors.New("an org needs an owner: make someone else an owner first"))
)

// Roles, ordered: each can do what the ones below it can.
var roleRank = map[string]int{"member": 1, "admin": 2, "owner": 3}

func roleName(r panelv1.Role) (string, error) {
	switch r {
	case panelv1.Role_ROLE_MEMBER:
		return "member", nil
	case panelv1.Role_ROLE_ADMIN:
		return "admin", nil
	case panelv1.Role_ROLE_OWNER:
		return "owner", nil
	}
	return "", connect.NewError(connect.CodeInvalidArgument, errors.New("which role?"))
}

func roleProto(r string) panelv1.Role {
	switch r {
	case "member":
		return panelv1.Role_ROLE_MEMBER
	case "admin":
		return panelv1.Role_ROLE_ADMIN
	case "owner":
		return panelv1.Role_ROLE_OWNER
	}
	return panelv1.Role_ROLE_UNSPECIFIED
}

func parseID(s, what string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("bad %s ID", what))
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func idString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

func orgName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || !utf8.ValidString(n) || utf8.RuneCountInString(n) > maxOrgName || strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("an org's name is 1 to %d characters", maxOrgName))
	}
	return n, nil
}

// member checks the caller is in org with at least role min. Outsiders get
// NOT_FOUND, so org IDs reveal nothing.
func (s *Service) member(ctx context.Context, q *store.Queries, orgID string, min string) (*auth.Session, pgtype.UUID, string, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, pgtype.UUID{}, "", err
	}
	org, err := parseID(orgID, "org")
	if err != nil {
		return nil, org, "", err
	}
	m, err := q.OrgMember(ctx, store.OrgMemberParams{OrgID: org, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, org, "", errNotFound
	}
	if err != nil {
		return nil, org, "", err
	}
	if roleRank[m.Role] < roleRank[min] {
		return nil, org, "", errDenied
	}
	return sess, org, m.Role, nil
}

// CreateOrg implements OrgService.
func (s *Service) CreateOrg(ctx context.Context, req *panelv1.CreateOrgRequest) (*panelv1.CreateOrgResponse, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	name, err := orgName(req.GetName())
	if err != nil {
		return nil, err
	}
	var out *panelv1.Org
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.CountOwnedOrgs(ctx, sess.UserID)
		if err != nil {
			return err
		}
		if n >= MaxOwnedOrgs {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("you can own up to %d orgs", MaxOwnedOrgs))
		}
		o, err := q.CreateOrg(ctx, name)
		if err != nil {
			return err
		}
		if err := q.AddOrgMember(ctx, store.AddOrgMemberParams{OrgID: o.ID, UserID: sess.UserID, Role: "owner"}); err != nil {
			return err
		}
		out = &panelv1.Org{Id: idString(o.ID), Name: o.Name, CreatedAt: timestamppb.New(o.CreatedAt.Time), Role: panelv1.Role_ROLE_OWNER}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.CreateOrgResponse{Org: out}, nil
}

// ListOrgs implements OrgService.
func (s *Service) ListOrgs(ctx context.Context, _ *panelv1.ListOrgsRequest) (*panelv1.ListOrgsResponse, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).UserOrgs(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListOrgsResponse{}
	for _, r := range rows {
		out.Orgs = append(out.Orgs, &panelv1.Org{Id: idString(r.ID), Name: r.Name, CreatedAt: timestamppb.New(r.CreatedAt.Time), Role: roleProto(r.Role)})
	}
	return out, nil
}

// RenameOrg implements OrgService.
func (s *Service) RenameOrg(ctx context.Context, req *panelv1.RenameOrgRequest) (*panelv1.RenameOrgResponse, error) {
	q := store.New(s.DB)
	_, org, _, err := s.member(ctx, q, req.GetOrgId(), "admin")
	if err != nil {
		return nil, err
	}
	name, err := orgName(req.GetName())
	if err != nil {
		return nil, err
	}
	if err := q.RenameOrg(ctx, store.RenameOrgParams{ID: org, Name: name}); err != nil {
		return nil, err
	}
	return &panelv1.RenameOrgResponse{}, nil
}

// ListMembers implements OrgService.
func (s *Service) ListMembers(ctx context.Context, req *panelv1.ListMembersRequest) (*panelv1.ListMembersResponse, error) {
	q := store.New(s.DB)
	_, org, _, err := s.member(ctx, q, req.GetOrgId(), "member")
	if err != nil {
		return nil, err
	}
	rows, err := q.OrgMembers(ctx, org)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListMembersResponse{}
	for _, r := range rows {
		out.Members = append(out.Members, &panelv1.Member{
			UserId: idString(r.UserID), Email: r.Email, Name: r.Name, Role: roleProto(r.Role), JoinedAt: timestamppb.New(r.CreatedAt.Time),
		})
	}
	return out, nil
}

// keepsOwner fails if org would be left without an owner once user stops
// being one. It locks the owners, so concurrent changes can't race past it.
func keepsOwner(ctx context.Context, q *store.Queries, org, user pgtype.UUID) error {
	owners, err := q.LockOrgOwners(ctx, org)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o != user {
			return nil
		}
	}
	return errOwner
}

// SetMemberRole implements OrgService.
func (s *Service) SetMemberRole(ctx context.Context, req *panelv1.SetMemberRoleRequest) (*panelv1.SetMemberRoleResponse, error) {
	role, err := roleName(req.GetRole())
	if err != nil {
		return nil, err
	}
	user, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		_, org, _, err := s.member(ctx, q, req.GetOrgId(), "owner")
		if err != nil {
			return err
		}
		if role != "owner" {
			if err := keepsOwner(ctx, q, org, user); err != nil {
				return err
			}
		}
		n, err := q.SetOrgMemberRole(ctx, store.SetOrgMemberRoleParams{OrgID: org, UserID: user, Role: role})
		if err != nil {
			return err
		}
		if n == 0 {
			return connect.NewError(connect.CodeNotFound, errors.New("they're not in this org"))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.SetMemberRoleResponse{}, nil
}

// RemoveMember implements OrgService.
func (s *Service) RemoveMember(ctx context.Context, req *panelv1.RemoveMemberRequest) (*panelv1.RemoveMemberResponse, error) {
	user, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		sess, org, myRole, err := s.member(ctx, q, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		target, err := q.OrgMember(ctx, store.OrgMemberParams{OrgID: org, UserID: user})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("they're not in this org"))
		}
		if err != nil {
			return err
		}
		// Leaving is always allowed; removing someone else needs a higher
		// role than theirs, except that owners can remove owners.
		if user != sess.UserID && myRole != "owner" && roleRank[myRole] <= roleRank[target.Role] {
			return errDenied
		}
		if target.Role == "owner" {
			if err := keepsOwner(ctx, q, org, user); err != nil {
				return err
			}
		}
		_, err = q.RemoveOrgMember(ctx, store.RemoveOrgMemberParams{OrgID: org, UserID: user})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.RemoveMemberResponse{}, nil
}

func invitationProto(i store.OrgInvitation) *panelv1.Invitation {
	return &panelv1.Invitation{
		Id: idString(i.ID), Email: i.Email, Role: roleProto(i.Role),
		CreatedAt: timestamppb.New(i.CreatedAt.Time), ExpiresAt: timestamppb.New(i.ExpiresAt.Time),
	}
}

// InviteMember implements OrgService.
func (s *Service) InviteMember(ctx context.Context, req *panelv1.InviteMemberRequest) (*panelv1.InviteMemberResponse, error) {
	q := store.New(s.DB)
	sess, org, myRole, err := s.member(ctx, q, req.GetOrgId(), "admin")
	if err != nil {
		return nil, err
	}
	role, err := roleName(req.GetRole())
	if err != nil {
		return nil, err
	}
	if roleRank[role] > roleRank[myRole] {
		return nil, errDenied
	}
	email, err := auth.NormalizeEmail(req.GetEmail())
	if err != nil {
		return nil, err
	}
	if s.Auth.Mailer == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("email isn't set up on this Panel"))
	}
	// Invitations send email on the org's behalf, so they're limited like
	// sign-in codes are.
	if err := s.Auth.RateLimit(ctx, "invite:org:"+idString(org), invitesPerDay, 24*time.Hour); err != nil {
		return nil, err
	}
	if n, err := q.CountPendingInvitations(ctx, org); err != nil {
		return nil, err
	} else if n >= MaxPending {
		return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("an org can have up to %d pending invitations", MaxPending))
	}
	o, err := q.GetOrg(ctx, org)
	if err != nil {
		return nil, err
	}
	token := auth.NewToken()
	inv, err := q.CreateInvitation(ctx, store.CreateInvitationParams{
		OrgID: org, Email: email, Role: role, TokenHash: auth.HashToken(token), InvitedBy: sess.UserID,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(InvitationTTL), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	// The token goes after #, like sign-in links, so it stays out of logs.
	link := strings.TrimSuffix(s.Auth.AppURL, "/") + "/invite#" + token
	body := fmt.Sprintf("%s invited you to join %q on Raptor as %s.\n\nAccept the invitation, signed in as %s:\n%s\n\nIt works for 7 days. If you weren't expecting it, ignore it.\n",
		sess.User.Email, o.Name, articled(role), email, link)
	if err := s.Auth.Mailer.Send(ctx, email, fmt.Sprintf("Join %s on Raptor", o.Name), body); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("couldn't send the email; try again in a minute"))
	}
	return &panelv1.InviteMemberResponse{Invitation: invitationProto(inv)}, nil
}

func articled(role string) string {
	if role == "admin" || role == "owner" {
		return "an " + role
	}
	return "a " + role
}

// ListInvitations implements OrgService.
func (s *Service) ListInvitations(ctx context.Context, req *panelv1.ListInvitationsRequest) (*panelv1.ListInvitationsResponse, error) {
	q := store.New(s.DB)
	_, org, _, err := s.member(ctx, q, req.GetOrgId(), "admin")
	if err != nil {
		return nil, err
	}
	rows, err := q.PendingInvitations(ctx, org)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListInvitationsResponse{}
	for _, r := range rows {
		out.Invitations = append(out.Invitations, invitationProto(r))
	}
	return out, nil
}

// RevokeInvitation implements OrgService.
func (s *Service) RevokeInvitation(ctx context.Context, req *panelv1.RevokeInvitationRequest) (*panelv1.RevokeInvitationResponse, error) {
	q := store.New(s.DB)
	_, org, _, err := s.member(ctx, q, req.GetOrgId(), "admin")
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.GetInvitationId(), "invitation")
	if err != nil {
		return nil, err
	}
	n, err := q.RevokeInvitation(ctx, store.RevokeInvitationParams{ID: id, OrgID: org})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such pending invitation"))
	}
	return &panelv1.RevokeInvitationResponse{}, nil
}

var errBadInvite = connect.NewError(connect.CodeNotFound, errors.New("that invitation is used, revoked, or expired; ask for a new one"))

// AcceptInvitation implements OrgService. The invitation is for an
// address, so a forwarded link is no use to anyone else.
func (s *Service) AcceptInvitation(ctx context.Context, req *panelv1.AcceptInvitationRequest) (*panelv1.AcceptInvitationResponse, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	var out *panelv1.Org
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		inv, err := q.InvitationByToken(ctx, auth.HashToken(req.GetToken()))
		if errors.Is(err, pgx.ErrNoRows) {
			return errBadInvite
		}
		if err != nil {
			return err
		}
		if inv.AcceptedAt.Valid || inv.RevokedAt.Valid || !inv.ExpiresAt.Time.After(s.now()) {
			return errBadInvite
		}
		if inv.Email != sess.User.Email || !sess.User.EmailVerifiedAt.Valid {
			return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("this invitation is for %s; sign in with that address to accept it", inv.Email))
		}
		if err := q.AcceptInvitation(ctx, inv.ID); err != nil {
			return err
		}
		// Already a member: keep the higher role.
		m, err := q.OrgMember(ctx, store.OrgMemberParams{OrgID: inv.OrgID, UserID: sess.UserID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := q.AddOrgMember(ctx, store.AddOrgMemberParams{OrgID: inv.OrgID, UserID: sess.UserID, Role: inv.Role}); err != nil {
				return err
			}
			m.Role = inv.Role
		case err != nil:
			return err
		case roleRank[inv.Role] > roleRank[m.Role]:
			if _, err := q.SetOrgMemberRole(ctx, store.SetOrgMemberRoleParams{OrgID: inv.OrgID, UserID: sess.UserID, Role: inv.Role}); err != nil {
				return err
			}
			m.Role = inv.Role
		}
		o, err := q.GetOrg(ctx, inv.OrgID)
		if err != nil {
			return err
		}
		out = &panelv1.Org{Id: idString(o.ID), Name: o.Name, CreatedAt: timestamppb.New(o.CreatedAt.Time), Role: roleProto(m.Role)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.AcceptInvitationResponse{Org: out}, nil
}

// CreateJoinToken implements OrgService.
func (s *Service) CreateJoinToken(ctx context.Context, req *panelv1.CreateJoinTokenRequest) (*panelv1.CreateJoinTokenResponse, error) {
	if s.Registry == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("nodes can't be linked to this Panel"))
	}
	sess, org, _, err := s.member(ctx, store.New(s.DB), req.GetOrgId(), "admin")
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RequireReauth(sess); err != nil {
		return nil, err
	}
	if err := s.Auth.RateLimit(ctx, "join:org:"+idString(org), joinTokensHour, time.Hour); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if utf8.RuneCountInString(name) > maxOrgName {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a node's name is up to %d characters", maxOrgName))
	}
	token, err := s.Registry.CreateJoinToken(ctx, idString(org), name)
	if err != nil {
		return nil, err
	}
	return &panelv1.CreateJoinTokenResponse{Token: token, ExpiresAt: timestamppb.New(s.now().Add(nodes.JoinTokenTTL))}, nil
}
