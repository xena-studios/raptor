// Package staff is Raptor's own admin panel (docs/PANEL.md#staff): platform
// insight, and looking after accounts and orgs. Staff are made with `panel
// staff add` on the Panel's server, never from the web. Every call needs a
// passkey confirmed in the last 12 hours; changes, one in the last 5
// minutes. Everything staff change goes in the staff log, which can't be
// edited, and in the affected account's or org's own log.
package staff

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
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
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/storage"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Roles.
const (
	Admin   = "admin"
	Support = "support"
)

// How recently a passkey must have been confirmed.
const (
	ViewFresh   = 12 * time.Hour
	ChangeFresh = 5 * time.Minute
)

// Service implements StaffService.
type Service struct {
	DB   *pgxpool.Pool
	Auth *auth.Service
	Log  *slog.Logger
	// BillingLive reports whether the Panel charges (billing.Service.Enabled;
	// nil: it doesn't).
	BillingLive func() bool
}

func (s *Service) billingLive() bool { return s.BillingLive != nil && s.BillingLive() }

var _ panelv1connect.StaffServiceHandler = (*Service)(nil)

var (
	errNotStaff   = connect.NewError(connect.CodePermissionDenied, errors.New("this is for Raptor's staff"))
	errNoPasskey  = connect.NewError(connect.CodePermissionDenied, errors.New("staff accounts need a passkey: add one in Settings, Security, then sign in with it"))
	errAdminsOnly = connect.NewError(connect.CodePermissionDenied, errors.New("only admins can do that; support can look"))
	errNotFound   = connect.NewError(connect.CodeNotFound, errors.New("not found"))
)

// caller is a staff member making a call.
type caller struct {
	sess *auth.Session
	role string
}

// staff checks the caller is staff with a fresh enough passkey: within
// ViewFresh to look, ChangeFresh to change (admins only).
func (s *Service) staff(ctx context.Context, change bool) (*caller, error) {
	if change {
		return s.check(ctx, ChangeFresh, true)
	}
	return s.check(ctx, ViewFresh, false)
}

// check checks the caller is staff (an admin, if adminOnly) whose session
// confirmed a passkey within fresh.
func (s *Service) check(ctx context.Context, fresh time.Duration, adminOnly bool) (*caller, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	role, err := q.StaffRole(ctx, sess.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotStaff
	}
	if err != nil {
		return nil, err
	}
	if n, err := q.CountPasskeys(ctx, sess.UserID); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errNoPasskey
	}
	if !sess.PasskeyAt.Valid || s.Auth.Clock().Sub(sess.PasskeyAt.Time) > fresh {
		return nil, auth.ErrReauth
	}
	if adminOnly && role != Admin {
		return nil, errAdminsOnly
	}
	return &caller{sess: sess, role: role}, nil
}

// viewed records that staff opened an account or org, once per 10 minutes.
func (s *Service) viewed(ctx context.Context, c *caller, action, targetType, targetID string) {
	if seen, err := store.New(s.DB).RecentStaffView(ctx, store.RecentStaffViewParams{StaffID: c.sess.UserID, Action: action, TargetID: targetID}); err == nil && seen {
		return
	}
	_ = s.audit(ctx, c, action, targetType, targetID, nil)
}

// audit records what staff did, in the staff log.
func (s *Service) audit(ctx context.Context, c *caller, action, targetType, targetID string, meta map[string]any) error {
	b := []byte("{}")
	if len(meta) > 0 {
		var err error
		if b, err = json.Marshal(meta); err != nil {
			return err
		}
	}
	var ip *netip.Addr
	if a := s.Auth.ClientIP(ctx); a.IsValid() {
		ip = &a
	}
	return store.New(s.DB).AddStaffAudit(ctx, store.AddStaffAuditParams{
		StaffID: c.sess.UserID, StaffEmail: c.sess.User.Email, Action: action, TargetType: targetType,
		TargetID: targetID, Meta: b, Ip: ip,
	})
}

func ts(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

func id(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func parseID(s, what string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, connect.NewError(connect.CodeInvalidArgument, errors.New("that isn't a "+what+" ID"))
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func reason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if r == "" || utf8.RuneCountInString(r) > 500 {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("give a reason, up to 500 characters: it's kept in the staff log and the account's own"))
	}
	return r, nil
}

// GetStaffMe implements StaffService.
func (s *Service) GetStaffMe(ctx context.Context, _ *panelv1.GetStaffMeRequest) (*panelv1.GetStaffMeResponse, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	role, err := store.New(s.DB).StaffRole(ctx, sess.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &panelv1.GetStaffMeResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	fresh := sess.PasskeyAt.Valid && s.Auth.Clock().Sub(sess.PasskeyAt.Time) <= ViewFresh
	return &panelv1.GetStaffMeResponse{Role: role, PasskeyFresh: fresh}, nil
}

// GetOverview implements StaffService.
func (s *Service) GetOverview(ctx context.Context, req *panelv1.GetOverviewRequest) (*panelv1.GetOverviewResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	days := req.GetDays()
	if days <= 0 {
		days = 30
	}
	days = min(days, 365)
	since := s.Auth.Clock().AddDate(0, 0, -int(days))
	q := store.New(s.DB)
	c, err := q.PlatformCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetOverviewResponse{
		Counts: &panelv1.PlatformCounts{
			Users: c.Users, Orgs: c.Orgs, Nodes: c.Nodes, NodesConnected: c.NodesConnected, Servers: c.Servers,
			ServersRunning: c.ServersRunning, StorageBytes: c.StorageBytes, Signups_30D: c.Signups30d,
			ActiveUsers_30D: c.ActiveUsers30d, StorageNodes: c.StorageNodes,
		},
	}
	hist, err := q.ListPlatformStats(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, h := range hist {
		out.History = append(out.History, &panelv1.PlatformPoint{
			At: ts(h.At), Users: h.Users, Orgs: h.Orgs, Nodes: h.Nodes, NodesConnected: h.NodesConnected,
			Servers: h.Servers, ServersRunning: h.ServersRunning, StorageBytes: h.StorageBytes,
		})
	}
	signups, err := q.SignupsByDay(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, d := range signups {
		out.SignupsByDay = append(out.SignupsByDay, &panelv1.CountBucket{Name: d.Day.Time.Format(time.DateOnly), Count: d.N})
	}
	versions, err := q.WingsVersions(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		out.WingsVersions = append(out.WingsVersions, &panelv1.CountBucket{Name: v.Version, Count: v.N})
	}
	archs, err := q.NodeArchs(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range archs {
		out.Archs = append(out.Archs, &panelv1.CountBucket{Name: a.Arch, Count: a.N})
	}
	eggs, err := q.TopEggs(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range eggs {
		out.TopEggs = append(out.TopEggs, &panelv1.CountBucket{Name: e.Egg, Count: e.N})
	}
	offline, err := q.OfflineNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range offline {
		out.OfflineNodes = append(out.OfflineNodes, &panelv1.OfflineNode{
			NodeId: id(n.ID), Name: n.Name, OrgId: id(n.OrgID), OrgName: n.OrgName, LastSeenAt: ts(n.LastSeenAt), WingsVersion: n.WingsVersion,
		})
	}
	failing, err := q.FailingServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range failing {
		state := f.State
		if f.InstallState == "failed" {
			state = "install_failed"
		}
		out.FailingServers = append(out.FailingServers, &panelv1.FailingServer{
			NodeId: id(f.NodeID), ServerId: f.ServerID, Name: f.Name, State: state, InstallError: f.InstallError,
			EggName: f.EggName, OrgId: id(f.OrgID), OrgName: f.OrgName,
		})
	}
	// Billing's plans and payments, and what Raptor Backup Storage would
	// bill a month (it isn't charged yet).
	storageOrgs, err := q.StorageByOrg(ctx)
	if err != nil {
		return nil, err
	}
	rev := &panelv1.Revenue{StorageOrgs: int32(len(storageOrgs)), BillingLive: s.billingLive()} //nolint:gosec // a count of orgs
	for _, o := range storageOrgs {
		rev.StorageCentsMonth += storage.Estimate(o.Bytes, int(o.Nodes))
	}
	plans, err := q.BillingPlanCounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		switch p.Status {
		case "active", "past_due":
			rev.MrrCents += int64(p.Seats) * billing.NodePriceCents
			rev.PayingOrgs += p.Orgs
		case "trialing":
			rev.TrialingOrgs += p.Orgs
		}
	}
	months, err := q.RevenueByMonth(ctx, pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -30), Valid: true})
	if err != nil {
		return nil, err
	}
	for _, m := range months {
		rev.RecentPaidCents += m.PaidCents - m.RefundedCents
	}
	out.Revenue = rev
	return out, nil
}

func staffUser(u store.SearchUsersRow) *panelv1.StaffUser {
	return &panelv1.StaffUser{
		Id: id(u.ID), Email: u.Email, Name: u.Name, CreatedAt: ts(u.CreatedAt), SuspendedAt: ts(u.SuspendedAt),
		Orgs: u.Orgs, Passkeys: u.Passkeys, LastSeenAt: ts(u.LastSeenAt), StaffRole: u.StaffRole,
	}
}

// SearchUsers implements StaffService.
func (s *Service) SearchUsers(ctx context.Context, req *panelv1.SearchUsersRequest) (*panelv1.SearchUsersResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).SearchUsers(ctx, store.SearchUsersParams{Query: strings.TrimSpace(req.GetQuery()), Skip: max(0, req.GetOffset())})
	if err != nil {
		return nil, err
	}
	out := &panelv1.SearchUsersResponse{}
	for _, r := range rows {
		out.Users = append(out.Users, staffUser(r))
	}
	return out, nil
}

// GetUser implements StaffService.
func (s *Service) GetUser(ctx context.Context, req *panelv1.GetUserRequest) (*panelv1.GetUserResponse, error) {
	c, err := s.staff(ctx, false)
	if err != nil {
		return nil, err
	}
	uid, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	rows, err := q.SearchUsers(ctx, store.SearchUsersParams{Query: id(uid)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errNotFound
	}
	user, err := q.GetUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetUserResponse{User: staffUser(rows[0])}
	out.User.SuspendedReason = user.SuspendedReason
	ms, err := q.UserMemberships(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		out.Memberships = append(out.Memberships, &panelv1.UserMembership{OrgId: id(m.ID), OrgName: m.Name, Role: m.Role, OrgSuspended: m.SuspendedAt.Valid})
	}
	ins, err := q.UserSignins(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, si := range ins {
		var meta struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(si.Metadata, &meta)
		in := &panelv1.UserSignin{At: ts(si.At), UserAgent: si.UserAgent, Method: meta.Method}
		if si.Ip != nil {
			in.Ip = si.Ip.String()
		}
		out.SignIns = append(out.SignIns, in)
	}
	// Looking someone up is recorded too: who looked at whom.
	s.viewed(ctx, c, "user.view", "user", id(uid))
	return out, nil
}

// SuspendUser implements StaffService.
func (s *Service) SuspendUser(ctx context.Context, req *panelv1.SuspendUserRequest) (*panelv1.SuspendUserResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	uid, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	why, err := reason(req.GetReason())
	if err != nil {
		return nil, err
	}
	if uid == c.sess.UserID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("you can't suspend yourself"))
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.SuspendUser(ctx, store.SuspendUserParams{ID: uid, SuspendedReason: why})
		if err != nil {
			return err
		}
		if n == 0 {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("that account doesn't exist or is already suspended"))
		}
		if _, err := q.RevokeUserSessions(ctx, uid); err != nil {
			return err
		}
		return s.Auth.Audit(ctx, q, auth.Event{User: uid, Actor: c.sess.UserID, Staff: true, Action: "account.suspend", Meta: map[string]any{"reason": why}})
	})
	if err != nil {
		return nil, err
	}
	_ = s.audit(ctx, c, "user.suspend", "user", id(uid), map[string]any{"reason": why})
	return &panelv1.SuspendUserResponse{}, nil
}

// UnsuspendUser implements StaffService.
func (s *Service) UnsuspendUser(ctx context.Context, req *panelv1.UnsuspendUserRequest) (*panelv1.UnsuspendUserResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	uid, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	if n, err := store.New(s.DB).UnsuspendUser(ctx, uid); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errNotFound
	}
	_ = s.Auth.Audit(ctx, nil, auth.Event{User: uid, Actor: c.sess.UserID, Staff: true, Action: "account.unsuspend"})
	_ = s.audit(ctx, c, "user.unsuspend", "user", id(uid), nil)
	return &panelv1.UnsuspendUserResponse{}, nil
}

// SignOutUser implements StaffService.
func (s *Service) SignOutUser(ctx context.Context, req *panelv1.SignOutUserRequest) (*panelv1.SignOutUserResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	uid, err := parseID(req.GetUserId(), "user")
	if err != nil {
		return nil, err
	}
	n, err := store.New(s.DB).RevokeUserSessions(ctx, uid)
	if err != nil {
		return nil, err
	}
	_ = s.Auth.Audit(ctx, nil, auth.Event{User: uid, Actor: c.sess.UserID, Staff: true, Action: "account.signout_all", Meta: map[string]any{"sessions": n}})
	_ = s.audit(ctx, c, "user.signout", "user", id(uid), map[string]any{"sessions": n})
	return &panelv1.SignOutUserResponse{Sessions: int32(n)}, nil //nolint:gosec // a count of sessions
}

func staffOrg(o store.SearchOrgsRow) *panelv1.StaffOrg {
	return &panelv1.StaffOrg{
		Id: id(o.ID), Name: o.Name, CreatedAt: ts(o.CreatedAt), SuspendedAt: ts(o.SuspendedAt),
		Members: o.Members, Nodes: o.Nodes, Servers: o.Servers,
	}
}

// SearchOrgs implements StaffService.
func (s *Service) SearchOrgs(ctx context.Context, req *panelv1.SearchOrgsRequest) (*panelv1.SearchOrgsResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).SearchOrgs(ctx, store.SearchOrgsParams{Query: strings.TrimSpace(req.GetQuery()), Skip: max(0, req.GetOffset())})
	if err != nil {
		return nil, err
	}
	out := &panelv1.SearchOrgsResponse{}
	for _, r := range rows {
		out.Orgs = append(out.Orgs, staffOrg(r))
	}
	return out, nil
}

// GetStaffOrg implements StaffService.
func (s *Service) GetStaffOrg(ctx context.Context, req *panelv1.GetStaffOrgRequest) (*panelv1.GetStaffOrgResponse, error) {
	c, err := s.staff(ctx, false)
	if err != nil {
		return nil, err
	}
	oid, err := parseID(req.GetOrgId(), "org")
	if err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	rows, err := q.SearchOrgs(ctx, store.SearchOrgsParams{Query: id(oid)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errNotFound
	}
	org, err := q.GetOrgForStaff(ctx, oid)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetStaffOrgResponse{Org: staffOrg(rows[0])}
	out.Org.SuspendedReason = org.SuspendedReason
	members, err := q.OrgMembersForStaff(ctx, oid)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		out.Members = append(out.Members, &panelv1.StaffOrgMember{UserId: id(m.ID), Email: m.Email, Name: m.Name, Role: m.Role})
	}
	nodes, err := q.OrgNodesForStaff(ctx, oid)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, &panelv1.StaffOrgNode{
			Id: id(n.ID), Name: n.Name, Connected: n.Connected, WingsVersion: n.WingsVersion, Arch: n.Arch,
			LastSeenAt: ts(n.LastSeenAt), CreatedAt: ts(n.CreatedAt), Servers: n.Servers,
		})
	}
	if u, err := q.LatestBackupStorageUsage(ctx, oid); err == nil {
		out.StorageBytes = u.Bytes
	}
	s.viewed(ctx, c, "org.view", "org", id(oid))
	return out, nil
}

// SuspendOrg implements StaffService.
func (s *Service) SuspendOrg(ctx context.Context, req *panelv1.SuspendOrgRequest) (*panelv1.SuspendOrgResponse, error) {
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
	if n, err := store.New(s.DB).SuspendOrg(ctx, store.SuspendOrgParams{ID: oid, SuspendedReason: why}); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("that org doesn't exist or is already suspended"))
	}
	_ = s.Auth.Audit(ctx, nil, auth.Event{Org: oid, Actor: c.sess.UserID, Staff: true, Action: "org.suspend", Meta: map[string]any{"reason": why}})
	_ = s.audit(ctx, c, "org.suspend", "org", id(oid), map[string]any{"reason": why})
	return &panelv1.SuspendOrgResponse{}, nil
}

// UnsuspendOrg implements StaffService.
func (s *Service) UnsuspendOrg(ctx context.Context, req *panelv1.UnsuspendOrgRequest) (*panelv1.UnsuspendOrgResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	oid, err := parseID(req.GetOrgId(), "org")
	if err != nil {
		return nil, err
	}
	if n, err := store.New(s.DB).UnsuspendOrg(ctx, oid); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errNotFound
	}
	_ = s.Auth.Audit(ctx, nil, auth.Event{Org: oid, Actor: c.sess.UserID, Staff: true, Action: "org.unsuspend"})
	_ = s.audit(ctx, c, "org.unsuspend", "org", id(oid), nil)
	return &panelv1.UnsuspendOrgResponse{}, nil
}

// ListStaff implements StaffService.
func (s *Service) ListStaff(ctx context.Context, _ *panelv1.ListStaffRequest) (*panelv1.ListStaffResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).ListStaff(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListStaffResponse{}
	for _, r := range rows {
		out.Staff = append(out.Staff, &panelv1.StaffMember{
			UserId: id(r.UserID), Email: r.Email, Name: r.Name, Role: r.Role, AddedAt: ts(r.AddedAt), AddedBy: r.AddedBy,
		})
	}
	return out, nil
}

// ListStaffAudit implements StaffService.
func (s *Service) ListStaffAudit(ctx context.Context, req *panelv1.ListStaffAuditRequest) (*panelv1.ListStaffAuditResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	var before pgtype.Timestamptz
	if t := req.GetPageToken(); t != "" {
		b, err := base64.RawURLEncoding.DecodeString(t)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad page token"))
		}
		at, err := time.Parse(time.RFC3339Nano, string(b))
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad page token"))
		}
		before = pgtype.Timestamptz{Time: at, Valid: true}
	}
	rows, err := store.New(s.DB).ListStaffAudit(ctx, before)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListStaffAuditResponse{}
	for _, r := range rows {
		e := &panelv1.StaffAuditEntry{
			Id: id(r.ID), At: ts(r.At), StaffEmail: r.StaffEmail, Action: r.Action, TargetType: r.TargetType,
			TargetId: r.TargetID, MetaJson: string(r.Meta),
		}
		if r.Ip != nil {
			e.Ip = r.Ip.String()
		}
		out.Entries = append(out.Entries, e)
	}
	if len(rows) == 50 {
		out.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(rows[49].At.Time.Format(time.RFC3339Nano)))
	}
	return out, nil
}

// Actor is a staff member acting through another service (the official
// egg library).
type Actor struct{ c *caller }

// Email is the staff member's email.
func (a *Actor) Email() string { return a.c.sess.User.Email }

// Admin reports whether the staff member is an admin (who can change
// things, after confirming their passkey).
func (a *Actor) Admin() bool { return a.c.role == "admin" }

// UserID is the staff member's account.
func (a *Actor) UserID() pgtype.UUID { return a.c.sess.UserID }

// Require checks the caller is staff, to look, or an admin who confirmed a
// passkey in the last ChangeFresh, to change something.
func (s *Service) Require(ctx context.Context, change bool) (*Actor, error) {
	c, err := s.staff(ctx, change)
	if err != nil {
		return nil, err
	}
	return &Actor{c: c}, nil
}

// Record adds an action to the staff log.
func (s *Service) Record(ctx context.Context, a *Actor, action, targetType, targetID string, meta map[string]any) error {
	return s.audit(ctx, a.c, action, targetType, targetID, meta)
}
