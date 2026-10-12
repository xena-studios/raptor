package staff

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Support access requests (docs/PANEL.md#support-access): staff ask, and
// the node's owner approves with their passkey (the grant Wings keeps) or
// denies. Staff then act under it through CommandService.Execute.

var levelNames = map[int32]string{1: "diagnostics", 2: "operate", 3: "manage"}

// RequestProto is a request as the API returns it. Approved requests that
// ran out are "expired".
func RequestProto(r store.SupportRequest, orgName, nodeName string, now time.Time, me pgtype.UUID) *panelv1.SupportRequest {
	status := r.Status
	if status == "approved" && r.ExpiresAt.Valid && !r.ExpiresAt.Time.After(now) {
		status = "expired"
	}
	out := &panelv1.SupportRequest{
		Id: id(r.ID), OrgId: id(r.OrgID), OrgName: orgName, NodeId: id(r.NodeID), NodeName: nodeName,
		StaffEmail: r.StaffEmail, Level: r.Level, Minutes: r.Minutes, Reason: r.Reason, Ticket: r.Ticket,
		Status: status, CreatedAt: ts(r.CreatedAt), DecidedAt: ts(r.DecidedAt), ExpiresAt: ts(r.ExpiresAt),
		Mine: me.Valid && r.StaffID == me, StaffId: id(r.StaffID),
	}
	if r.GrantedLevel.Valid {
		out.GrantedLevel = r.GrantedLevel.Int32
	}
	return out
}

// RequestSupportAccess implements StaffService. Admins and support may
// ask, with a passkey confirmed in the last 5 minutes.
func (s *Service) RequestSupportAccess(ctx context.Context, req *panelv1.RequestSupportAccessRequest) (*panelv1.RequestSupportAccessResponse, error) {
	c, err := s.check(ctx, ChangeFresh, false)
	if err != nil {
		return nil, err
	}
	nid, err := parseID(req.GetNodeId(), "node")
	if err != nil {
		return nil, err
	}
	level, minutes := req.GetLevel(), req.GetMinutes()
	reasonText, ticket := strings.TrimSpace(req.GetReason()), strings.TrimSpace(req.GetTicket())
	switch {
	case level < 1 || level > 3:
		return nil, invalid("the level is 1 (diagnostics), 2 (operate), or 3 (manage)")
	case minutes < 15 || minutes > 7*24*60:
		return nil, invalid("ask for 15 minutes to 7 days")
	case reasonText == "" || utf8.RuneCountInString(reasonText) > 500:
		return nil, invalid("say why, up to 500 characters: the owner reads it")
	case ticket == "" || utf8.RuneCountInString(ticket) > 100:
		return nil, invalid("give the ticket it's for")
	}
	q := store.New(s.DB)
	node, err := q.NodeNameOrg(ctx, nid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	r, err := q.CreateSupportRequest(ctx, store.CreateSupportRequestParams{
		OrgID: node.OrgID, NodeID: nid, StaffID: c.sess.UserID, StaffEmail: c.sess.User.Email,
		Level: level, Minutes: minutes, Reason: reasonText, Ticket: ticket,
	})
	if err != nil {
		return nil, err
	}
	meta := map[string]any{"node": id(nid), "level": level, "minutes": minutes, "reason": reasonText, "ticket": ticket}
	_ = s.Auth.Audit(ctx, nil, auth.Event{Org: node.OrgID, Actor: c.sess.UserID, Staff: true, Action: "support.request", Target: id(r.ID), Meta: meta})
	_ = s.audit(ctx, c, "support.request", "node", id(nid), meta)
	s.emailOwners(ctx, node.OrgID, node.Name, node.OrgName, r)
	return &panelv1.RequestSupportAccessResponse{Id: id(r.ID)}, nil
}

// emailOwners tells an org's owners and admins that staff asked.
func (s *Service) emailOwners(ctx context.Context, org pgtype.UUID, nodeName, orgName string, r store.SupportRequest) {
	if s.Auth.Mailer == nil {
		return
	}
	emails, err := store.New(s.DB).OrgAdminEmails(ctx, org)
	if err != nil {
		return
	}
	link := fmt.Sprintf("%s/orgs/%s/nodes/%s", strings.TrimRight(s.Auth.AppURL, "/"), id(org), id(r.NodeID))
	body := fmt.Sprintf(`Raptor support (%s) asks for access to %s, in %s.

What: %s (level %d) for %s
Why: %s
Ticket: %s

Nothing happens unless you approve it, with your passkey: %s

You can approve less than they asked for, deny it, and end it any time. On the node, sudo raptor support off turns support access off.
`, r.StaffEmail, nodeName, orgName, levelNames[r.Level], r.Level, duration(r.Minutes), r.Reason, r.Ticket, link)
	for _, e := range emails {
		if err := s.Auth.Mailer.Send(context.WithoutCancel(ctx), e, "Raptor support asks for access to "+nodeName, body); err != nil && s.Log != nil {
			s.Log.Warn("emailing a support request failed", "err", err)
		}
	}
}

func duration(minutes int32) string {
	switch {
	case minutes%(24*60) == 0:
		d := minutes / (24 * 60)
		if d == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", d)
	case minutes%60 == 0:
		h := minutes / 60
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// ListSupportRequests implements StaffService.
func (s *Service) ListSupportRequests(ctx context.Context, _ *panelv1.ListSupportRequestsRequest) (*panelv1.ListSupportRequestsResponse, error) {
	c, err := s.staff(ctx, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).StaffSupportRequests(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListSupportRequestsResponse{}
	now := s.Auth.Clock()
	for _, r := range rows {
		out.Requests = append(out.Requests, RequestProto(store.SupportRequest{
			ID: r.ID, OrgID: r.OrgID, NodeID: r.NodeID, StaffID: r.StaffID, StaffEmail: r.StaffEmail, Level: r.Level,
			Minutes: r.Minutes, Reason: r.Reason, Ticket: r.Ticket, Status: r.Status, CreatedAt: r.CreatedAt,
			DecidedAt: r.DecidedAt, DecidedBy: r.DecidedBy, GrantedLevel: r.GrantedLevel, ExpiresAt: r.ExpiresAt,
		}, r.OrgName, r.NodeName, now, c.sess.UserID))
	}
	return out, nil
}
