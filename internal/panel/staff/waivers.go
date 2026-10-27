package staff

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Billing waivers (DECISIONS #334): an admin gives an org nodes free, and
// optionally its Backup Storage past what's included. The billing sweep
// does the rest: seats are the nodes left, and an org whose nodes are all
// free needs no plan.

// maxFreeNodes bounds a waiver's node count (Polar's seats go to 10,000).
const maxFreeNodes = 10000

// waiverMeta is what a waiver gives, for the ledger and the logs; the
// reason only goes in the staff log.
func waiverMeta(w store.BillingWaiver) map[string]any {
	m := map[string]any{"all_nodes": w.AllNodes, "free_nodes": w.FreeNodes, "storage": w.Storage}
	if w.EndsAt.Valid {
		m["ends_at"] = w.EndsAt.Time.UTC().Format(time.RFC3339)
	}
	return m
}

func addLedger(ctx context.Context, q *store.Queries, org pgtype.UUID, kind string, w store.BillingWaiver, by string) error {
	m := waiverMeta(w)
	m["waiver"], m["by"] = id(w.ID), by
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return q.AddLedger(ctx, store.AddLedgerParams{OrgID: org, Kind: kind, Quantity: w.FreeNodes, Data: b})
}

func (s *Service) billingChanged(org pgtype.UUID) {
	if s.BillingChanged != nil {
		s.BillingChanged(uuid.UUID(org.Bytes))
	}
}

// SetBillingWaiver implements StaffService.
func (s *Service) SetBillingWaiver(ctx context.Context, req *panelv1.SetBillingWaiverRequest) (*panelv1.SetBillingWaiverResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	oid, err := parseID(req.GetOrgId(), "org")
	if err != nil {
		return nil, err
	}
	why, err := reason(req.GetReason())
	if err != nil {
		return nil, err
	}
	free := req.GetFreeNodes()
	if req.GetAllNodes() {
		free = 0
	}
	switch {
	case free < 0 || free > maxFreeNodes:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("free nodes are 1 to 10,000"))
	case !req.GetAllNodes() && free == 0 && !req.GetStorage():
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a waiver makes some nodes free, or Backup Storage, or both"))
	}
	var ends pgtype.Timestamptz
	if req.GetEndsAt() != nil {
		t := req.GetEndsAt().AsTime()
		if !t.After(s.Auth.Clock()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the end date has to be in the future"))
		}
		ends = pgtype.Timestamptz{Time: t, Valid: true}
	}
	var saved store.BillingWaiver
	var changed bool
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.GetOrgForStaff(ctx, oid); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		cur, err := q.ActiveWaiver(ctx, oid)
		switch {
		case err == nil:
			changed = true
			saved, err = q.UpdateWaiver(ctx, store.UpdateWaiverParams{
				ID: cur.ID, AllNodes: req.GetAllNodes(), FreeNodes: free, Storage: req.GetStorage(), Reason: why, EndsAt: ends,
			})
		case errors.Is(err, pgx.ErrNoRows):
			saved, err = q.InsertWaiver(ctx, store.InsertWaiverParams{
				OrgID: oid, AllNodes: req.GetAllNodes(), FreeNodes: free, Storage: req.GetStorage(), Reason: why, EndsAt: ends,
				CreatedBy: c.sess.UserID,
			})
		}
		if err != nil {
			return err
		}
		kind := "waiver.set"
		if changed {
			kind = "waiver.changed"
		}
		if err := addLedger(ctx, q, oid, kind, saved, c.sess.User.Email); err != nil {
			return err
		}
		// The org's own log says what, not why.
		action := "billing.waiver.set"
		if changed {
			action = "billing.waiver.change"
		}
		return s.Auth.Audit(ctx, q, auth.Event{Org: oid, Actor: c.sess.UserID, Staff: true, Action: action, Meta: waiverMeta(saved)})
	})
	if err != nil {
		return nil, err
	}
	meta := waiverMeta(saved)
	meta["reason"] = why
	action := "org.waiver.set"
	if changed {
		action = "org.waiver.change"
	}
	_ = s.audit(ctx, c, action, "org", id(oid), meta)
	s.billingChanged(oid)
	return &panelv1.SetBillingWaiverResponse{}, nil
}

// EndBillingWaiver implements StaffService.
func (s *Service) EndBillingWaiver(ctx context.Context, req *panelv1.EndBillingWaiverRequest) (*panelv1.EndBillingWaiverResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	oid, err := parseID(req.GetOrgId(), "org")
	if err != nil {
		return nil, err
	}
	var cur store.BillingWaiver
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		cur, err = q.ActiveWaiver(ctx, oid)
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("this org has no waiver"))
		} else if err != nil {
			return err
		}
		if _, err := q.EndWaiver(ctx, store.EndWaiverParams{ID: cur.ID, EndedAt: pgtype.Timestamptz{Time: s.Auth.Clock(), Valid: true}}); err != nil {
			return err
		}
		if err := addLedger(ctx, q, oid, "waiver.ended", cur, c.sess.User.Email); err != nil {
			return err
		}
		return s.Auth.Audit(ctx, q, auth.Event{Org: oid, Actor: c.sess.UserID, Staff: true, Action: "billing.waiver.end", Meta: waiverMeta(cur)})
	})
	if err != nil {
		return nil, err
	}
	_ = s.audit(ctx, c, "org.waiver.end", "org", id(oid), waiverMeta(cur))
	s.billingChanged(oid)
	return &panelv1.EndBillingWaiverResponse{}, nil
}

// staffWaiver is the org's waiver in force for its staff page, if any.
func staffWaiver(ctx context.Context, q *store.Queries, org pgtype.UUID) (*panelv1.StaffWaiver, error) {
	w, err := q.ActiveWaiver(ctx, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &panelv1.StaffWaiver{
		AllNodes: w.AllNodes, FreeNodes: w.FreeNodes, Storage: w.Storage, EndsAt: ts(w.EndsAt), Reason: w.Reason,
		CreatedAt: ts(w.CreatedAt), UpdatedAt: ts(w.UpdatedAt),
	}
	if w.CreatedBy.Valid {
		if u, err := q.GetUser(ctx, w.CreatedBy); err == nil {
			out.CreatedBy = u.Email
		}
	}
	return out, nil
}
