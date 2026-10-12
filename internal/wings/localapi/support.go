package localapi

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

// Support access from the box (raptor support): the owner can see it, end
// it, and turn it off without the Panel.

var errNoSupport = connect.NewError(connect.CodeUnavailable, errors.New("support access isn't available yet: Wings is starting"))

func msTime(ms int64) *timestamppb.Timestamp {
	if ms == 0 {
		return nil
	}
	return timestamppb.New(time.UnixMilli(ms))
}

// ListSupport implements LocalService. The raptor group may look.
func (s *Service) ListSupport(ctx context.Context, _ *localv1.ListSupportRequest) (*localv1.ListSupportResponse, error) {
	if s.Support == nil {
		return nil, errNoSupport
	}
	grants, err := s.Support.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &localv1.ListSupportResponse{Disabled: s.Support.Disabled(ctx)}
	now := time.Now()
	for _, g := range grants {
		out.Grants = append(out.Grants, &localv1.SupportGrant{
			Id: g.ID, StaffEmail: g.StaffEmail, Level: int32(g.Level), Reason: g.Reason, Ticket: g.Ticket, //nolint:gosec // 1 to 3
			GrantedBy: g.GrantedBy, CreatedAt: msTime(g.CreatedAt), ExpiresAt: msTime(g.ExpiresAt),
			RevokedAt: msTime(g.RevokedAt), Active: g.Active(now),
		})
	}
	return out, nil
}

// RevokeSupport implements LocalService. Root only.
func (s *Service) RevokeSupport(ctx context.Context, req *localv1.RevokeSupportRequest) (*localv1.RevokeSupportResponse, error) {
	if err := requireRoot(ctx, "end support access"); err != nil {
		return nil, err
	}
	if s.Support == nil {
		return nil, errNoSupport
	}
	if req.GetAll() {
		n, err := s.Support.RevokeAll(ctx, "local:root")
		return &localv1.RevokeSupportResponse{Revoked: n}, err
	}
	if req.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("say which grant, or all"))
	}
	if err := s.Support.Revoke(ctx, req.GetId(), "local:root"); err != nil {
		return nil, err
	}
	return &localv1.RevokeSupportResponse{Revoked: 1}, nil
}

// SetSupportEnabled implements LocalService. Root only.
func (s *Service) SetSupportEnabled(ctx context.Context, req *localv1.SetSupportEnabledRequest) (*localv1.SetSupportEnabledResponse, error) {
	if err := requireRoot(ctx, "change support access"); err != nil {
		return nil, err
	}
	if s.Support == nil {
		return nil, errNoSupport
	}
	return &localv1.SetSupportEnabledResponse{}, s.Support.SetDisabled(ctx, !req.GetEnabled(), "local:root")
}
