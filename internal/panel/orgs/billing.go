package orgs

import (
	"context"
	"errors"

	"connectrpc.com/connect"

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
	if w := st.Waiver; w != nil {
		out.Waiver = &panelv1.BillingWaiver{AllNodes: w.AllNodes, FreeNodes: int32(w.FreeNodes), Storage: w.Storage} //nolint:gosec // at most 10000
		if !w.EndsAt.IsZero() {
			out.Waiver.EndsAt = timestamppb.New(w.EndsAt)
		}
		out.FreeNodes, out.FreeNextNode = int32(st.Nodes-st.Billable), st.FreeNextNode() //nolint:gosec // node counts are small
	}
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
	return &panelv1.StartCheckoutResponse{Url: url, PolarOrigin: b.Orgs.Billing.Polar.Origin()}, nil
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

// owner is as for an owner who confirmed it's them in the last minutes.
func (b *Billing) owner(ctx context.Context, orgID string) (*auth.Session, uuid.UUID, error) {
	sess, org, err := b.as(ctx, orgID, "owner")
	if err != nil {
		return nil, org, err
	}
	if err := b.Orgs.Auth.RequireReauth(sess); err != nil {
		return nil, org, err
	}
	return sess, org, nil
}

// polarID checks an ID from the app before it goes into a Polar path.
func polarID(id, what string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("bad "+what+" ID"))
	}
	return id, nil
}

// GetBillingDetails implements BillingService.
func (b *Billing) GetBillingDetails(ctx context.Context, req *panelv1.GetBillingDetailsRequest) (*panelv1.GetBillingDetailsResponse, error) {
	_, org, err := b.as(ctx, req.GetOrgId(), "owner")
	if err != nil {
		return nil, err
	}
	d, err := b.Orgs.Billing.Details(ctx, org)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetBillingDetailsResponse{}
	for _, m := range d.Methods {
		out.Cards = append(out.Cards, &panelv1.Card{
			Id: m.ID, Type: m.Type, Brand: m.Metadata.Brand, Last4: m.Metadata.Last4,
			ExpMonth: int32(m.Metadata.ExpMonth), ExpYear: int32(m.Metadata.ExpYear), //nolint:gosec // months and years
			Wallet: m.Metadata.Wallet, Default: m.ID == d.DefaultID,
		})
	}
	for _, o := range d.Orders {
		out.Charges = append(out.Charges, &panelv1.Charge{
			Id: o.ID, At: timestamppb.New(o.CreatedAt), Status: o.Status, TotalCents: o.TotalAmount,
			RefundedCents: o.RefundedAmount, Currency: o.Currency, Reason: o.BillingReason,
			Description: o.Description, InvoiceNumber: o.InvoiceNumber,
		})
	}
	return out, nil
}

// StartAddCard implements BillingService.
func (b *Billing) StartAddCard(ctx context.Context, req *panelv1.StartAddCardRequest) (*panelv1.StartAddCardResponse, error) {
	sess, org, err := b.owner(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	tok, origin, err := b.Orgs.Billing.PaymentSession(ctx, org)
	if err != nil {
		return nil, err
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, "billing.add_card", "", nil, nil)
	return &panelv1.StartAddCardResponse{SessionToken: tok, PolarOrigin: origin}, nil
}

// SetDefaultCard implements BillingService.
func (b *Billing) SetDefaultCard(ctx context.Context, req *panelv1.SetDefaultCardRequest) (*panelv1.SetDefaultCardResponse, error) {
	sess, org, err := b.owner(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	id, err := polarID(req.GetCardId(), "card")
	if err != nil {
		return nil, err
	}
	if err := b.Orgs.Billing.SetDefaultPaymentMethod(ctx, org, id); err != nil {
		return nil, err
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, "billing.default_card", id, nil, nil)
	return &panelv1.SetDefaultCardResponse{}, nil
}

// RemoveCard implements BillingService.
func (b *Billing) RemoveCard(ctx context.Context, req *panelv1.RemoveCardRequest) (*panelv1.RemoveCardResponse, error) {
	sess, org, err := b.owner(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	id, err := polarID(req.GetCardId(), "card")
	if err != nil {
		return nil, err
	}
	if err := b.Orgs.Billing.RemovePaymentMethod(ctx, org, id); err != nil {
		return nil, err
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, "billing.remove_card", id, nil, nil)
	return &panelv1.RemoveCardResponse{}, nil
}

// GetInvoice implements BillingService.
func (b *Billing) GetInvoice(ctx context.Context, req *panelv1.GetInvoiceRequest) (*panelv1.GetInvoiceResponse, error) {
	_, org, err := b.as(ctx, req.GetOrgId(), "owner")
	if err != nil {
		return nil, err
	}
	id, err := polarID(req.GetChargeId(), "charge")
	if err != nil {
		return nil, err
	}
	url, err := b.Orgs.Billing.Invoice(ctx, org, id)
	if err != nil {
		return nil, err
	}
	return &panelv1.GetInvoiceResponse{Url: url}, nil
}

// CancelPlan implements BillingService.
func (b *Billing) CancelPlan(ctx context.Context, req *panelv1.CancelPlanRequest) (*panelv1.CancelPlanResponse, error) {
	sess, org, err := b.owner(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err := b.Orgs.Billing.Cancel(ctx, org, req.GetResume(), req.GetReason(), req.GetComment()); err != nil {
		return nil, err
	}
	action := "billing.cancel"
	if req.GetResume() {
		action = "billing.resume"
	}
	_ = b.Orgs.audit(ctx, nil, sess, pgtype.UUID{Bytes: org, Valid: true}, action, "", nil, nil)
	return &panelv1.CancelPlanResponse{}, nil
}
