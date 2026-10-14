package orgs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Billing implements BillingService (docs/PANEL.md#billing-polar).
type Billing struct {
	Orgs *Service
}

var _ panelv1connect.BillingServiceHandler = (*Billing)(nil)

// as checks the user's role in the org.
func (b *Billing) as(ctx context.Context, orgID, role string) (*auth.Session, uuid.UUID, error) {
	var sess *auth.Session
	var org pgtype.UUID
	err := b.Orgs.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		var err error
		org, _, err = member(ctx, q, se, orgID, role)
		return err
	})
	return sess, uuid.UUID(org.Bytes), err
}

func stamp(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

// GetBilling implements BillingService.
func (b *Billing) GetBilling(ctx context.Context, req *panelv1.GetBillingRequest) (*panelv1.GetBillingResponse, error) {
	_, org, err := b.as(ctx, req.GetOrgId(), "member")
	if err != nil {
		return nil, err
	}
	st, err := b.Orgs.Billing.State(ctx, org)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetBillingResponse{Enabled: st.Enabled}
	if !st.Enabled {
		return out, nil
	}
	a := st.Account
	out.Status, out.Seats, out.Nodes, out.NodePriceCents = a.Status, a.Seats, int32(st.Nodes), billing.NodePriceCents //nolint:gosec // node counts are small
	out.CurrentPeriodEnd, out.TrialEnd, out.CancelAtPeriodEnd = stamp(a.CurrentPeriodEnd), stamp(a.TrialEnd), a.CancelAtPeriodEnd
	if !st.ReadOnlyAt.IsZero() {
		out.ReadOnlyAt = timestamppb.New(st.ReadOnlyAt)
	}
	out.ReadOnly, out.TrialAvailable = st.ReadOnly, st.TrialAvailable
	return out, nil
}

// StartCheckout implements BillingService.
func (b *Billing) StartCheckout(ctx context.Context, req *panelv1.StartCheckoutRequest) (*panelv1.StartCheckoutResponse, error) {
	sess, org, err := b.as(ctx, req.GetOrgId(), "owner")
	if err != nil {
		return nil, err
	}
	url, err := b.Orgs.Billing.Checkout(ctx, org, sess.User.Email)
	if err != nil {
		return nil, err
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, "billing.checkout", "", nil, nil)
	return &panelv1.StartCheckoutResponse{Url: url}, nil
}

// OpenBillingPortal implements BillingService.
func (b *Billing) OpenBillingPortal(ctx context.Context, req *panelv1.OpenBillingPortalRequest) (*panelv1.OpenBillingPortalResponse, error) {
	_, org, err := b.as(ctx, req.GetOrgId(), "owner")
	if err != nil {
		return nil, err
	}
	url, err := b.Orgs.Billing.Portal(ctx, org)
	if err != nil {
		return nil, err
	}
	return &panelv1.OpenBillingPortalResponse{Url: url}, nil
}

// EndTrial implements BillingService.
func (b *Billing) EndTrial(ctx context.Context, req *panelv1.EndTrialRequest) (*panelv1.EndTrialResponse, error) {
	sess, org, err := b.as(ctx, req.GetOrgId(), "owner")
	if err != nil {
		return nil, err
	}
	if err := b.Orgs.Billing.EndTrial(ctx, org); err != nil {
		return nil, err
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, "billing.end_trial", "", nil, nil)
	return &panelv1.EndTrialResponse{}, nil
}
