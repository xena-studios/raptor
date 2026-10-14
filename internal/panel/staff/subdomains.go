package staff

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/subdomains"
)

// BanSubdomain implements StaffService: the name is released (its records
// go on the next sync) and can't be claimed again.
func (s *Service) BanSubdomain(ctx context.Context, req *panelv1.BanSubdomainRequest) (*panelv1.BanSubdomainResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(req.GetName())), ".")
	if errors.Is(subdomains.CheckName(name), subdomains.ErrName) {
		return nil, connect.NewError(connect.CodeInvalidArgument, subdomains.ErrName)
	}
	why, err := reason(req.GetReason())
	if err != nil {
		return nil, err
	}
	out := &panelv1.BanSubdomainResponse{}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.BanSubdomain(ctx, store.BanSubdomainParams{Name: name, Reason: why, StaffID: c.sess.UserID}); err != nil {
			return err
		}
		r, err := q.ReleaseSubdomainByName(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.OrgId = id(r.OrgID)
		return s.Auth.Audit(ctx, q, auth.Event{Org: r.OrgID, Actor: c.sess.UserID, Staff: true, Action: "subdomain.ban", Target: name, Meta: map[string]any{"reason": why}})
	})
	if err != nil {
		return nil, err
	}
	_ = s.audit(ctx, c, "subdomain.ban", "subdomain", name, map[string]any{"reason": why, "org": out.GetOrgId()})
	if s.SyncSubdomains != nil {
		go s.SyncSubdomains()
	}
	return out, nil
}
