package commands

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Support access (docs/PANEL.md#support-access). Staff send commands under
// a support request their owner approved; the Panel checks it, puts its ID
// in the signed grant, and Wings checks it again against the grant the
// owner's passkey signed. Owners approve by signing support.grant, which
// the Panel checks against the request before sending.

// supportFresh is how recently staff must have confirmed a passkey to act
// under support access (as for the admin panel).
const supportFresh = 12 * time.Hour

var errNoSupportAccess = connect.NewError(connect.CodePermissionDenied, errors.New("that support access isn't approved for you on this node, or it's over"))

// authorizeSupport allows a staff member's command under a support request.
func (s *Service) authorizeSupport(ctx context.Context, node pgtype.UUID, requestID string, env *nodecmd.Envelope) (*auth.Session, pgtype.UUID, error) {
	var org pgtype.UUID
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, org, err
	}
	q := store.New(s.Auth.DB)
	if _, err := q.StaffRole(ctx, sess.UserID); err != nil {
		return nil, org, errNoSupportAccess
	}
	if !sess.PasskeyAt.Valid || s.now().Sub(sess.PasskeyAt.Time) > supportFresh {
		return nil, org, auth.ErrReauth
	}
	rid, err := uuid.Parse(requestID)
	if err != nil {
		return nil, org, errNoSupportAccess
	}
	r, err := q.GetSupportRequest(ctx, pgtype.UUID{Bytes: rid, Valid: true})
	if err != nil {
		return nil, org, errNoSupportAccess
	}
	level := nodecmd.SupportLevel(env.Action)
	switch {
	case r.NodeID != node, r.StaffID != sess.UserID, r.Status != "approved",
		!r.ExpiresAt.Valid, !r.ExpiresAt.Time.After(s.now()), !r.GrantedLevel.Valid:
		return nil, org, errNoSupportAccess
	case env.Signature != nil, level == 0 || level > int(r.GrantedLevel.Int32):
		return nil, org, connect.NewError(connect.CodePermissionDenied, errors.New("the support access granted doesn't cover that"))
	}
	env.Grant.Support = rid.String()
	return sess, r.OrgID, nil
}

// grantParams are support.grant's params, as the owner's passkey signs them.
type grantParams struct {
	ID         string `json:"id"`
	StaffID    string `json:"staff_id"`
	StaffEmail string `json:"staff_email"`
	Level      int32  `json:"level"`
	ExpiresAt  int64  `json:"expires_at"` // unix ms
}

// checkGrant checks an owner's support.grant against the request it
// approves: the staff member who asked, no more than they asked for.
func (s *Service) checkSupportGrant(ctx context.Context, node pgtype.UUID, env nodecmd.Envelope) (grantParams, error) {
	var p grantParams
	bad := connect.NewError(connect.CodeInvalidArgument, errors.New("that isn't a support request for this node, or it's been decided already"))
	if err := json.Unmarshal(env.Params, &p); err != nil {
		return p, bad
	}
	rid, err := uuid.Parse(p.ID)
	if err != nil {
		return p, bad
	}
	r, err := store.New(s.Auth.DB).GetSupportRequest(ctx, pgtype.UUID{Bytes: rid, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (r.NodeID != node || r.Status != "pending")) {
		return p, bad
	}
	if err != nil {
		return p, err
	}
	latest := s.now().Add(time.Duration(r.Minutes)*time.Minute + 10*time.Minute)
	if p.StaffID != uuid.UUID(r.StaffID.Bytes).String() || p.StaffEmail != r.StaffEmail || p.Level < 1 || p.Level > r.Level ||
		time.UnixMilli(p.ExpiresAt).After(latest) {
		return p, connect.NewError(connect.CodeInvalidArgument, errors.New("support access can't be more than was asked for, or for someone else"))
	}
	return p, nil
}

// afterSupport records what the node did with support access.
func (s *Service) afterSupport(ctx context.Context, sess *auth.Session, node pgtype.UUID, env nodecmd.Envelope, granted grantParams) {
	q := store.New(s.Auth.DB)
	ctx = context.WithoutCancel(ctx)
	switch env.Action {
	case "support.grant":
		rid, _ := uuid.Parse(granted.ID)
		_, _ = q.ApproveSupportRequest(ctx, store.ApproveSupportRequestParams{
			ID: pgtype.UUID{Bytes: rid, Valid: true}, DecidedBy: sess.UserID, NodeID: node,
			GrantedLevel: pgtype.Int4{Int32: granted.Level, Valid: true},
			ExpiresAt:    pgtype.Timestamptz{Time: time.UnixMilli(granted.ExpiresAt), Valid: true},
		})
	case "support.revoke":
		var p struct {
			ID  string `json:"id"`
			All bool   `json:"all"`
		}
		_ = json.Unmarshal(env.Params, &p)
		if p.All {
			_, _ = q.RevokeNodeSupportRequests(ctx, node)
		} else if rid, err := uuid.Parse(p.ID); err == nil {
			_, _ = q.RevokeSupportRequest(ctx, store.RevokeSupportRequestParams{ID: pgtype.UUID{Bytes: rid, Valid: true}, NodeID: node})
		}
	case "support.disable":
		var p struct {
			Off bool `json:"off"`
		}
		if json.Unmarshal(env.Params, &p) == nil && p.Off {
			_, _ = q.RevokeNodeSupportRequests(ctx, node)
		}
	}
}
