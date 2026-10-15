package staff

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/rollout"
	"github.com/xena-studios/raptor/internal/panel/storage"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/shared/hosted"
)

// The overview's tabs (docs/PANEL.md#staff): users, nodes, billing, and the
// Panel's own infrastructure, each in more depth than the summary.

// since is how far back a tab's graphs go: days (default 30, at most a
// year) before now.
func (s *Service) since(days int32) time.Time {
	if days <= 0 {
		days = 30
	}
	return s.Auth.Clock().AddDate(0, 0, -int(min(days, 365)))
}

func (s *Service) history(ctx context.Context, since time.Time) ([]*panelv1.PlatformPoint, error) {
	rows, err := store.New(s.DB).ListPlatformStats(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]*panelv1.PlatformPoint, 0, len(rows))
	for _, h := range rows {
		out = append(out, &panelv1.PlatformPoint{
			At: ts(h.At), Users: h.Users, Orgs: h.Orgs, Nodes: h.Nodes, NodesConnected: h.NodesConnected,
			Servers: h.Servers, ServersRunning: h.ServersRunning, StorageBytes: h.StorageBytes,
		})
	}
	return out, nil
}

func days(rows []store.SignupsByDayRow) []*panelv1.CountBucket {
	out := make([]*panelv1.CountBucket, 0, len(rows))
	for _, d := range rows {
		out = append(out, &panelv1.CountBucket{Name: d.Day.Time.Format(time.DateOnly), Count: d.N})
	}
	return out
}

// GetUserInsights implements StaffService.
func (s *Service) GetUserInsights(ctx context.Context, req *panelv1.GetUserInsightsRequest) (*panelv1.GetUserInsightsResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	since := s.since(req.GetDays())
	q := store.New(s.DB)
	c, err := q.UserInsightCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetUserInsightsResponse{
		Users: c.Users, Active_1D: c.Active1d, Active_7D: c.Active7d, Active_30D: c.Active30d,
		WithPasskey: c.WithPasskey, WithTotp: c.WithTotp, Suspended: c.Suspended, WithoutOrg: c.WithoutOrg,
		OpenInvitations: c.OpenInvitations,
	}
	methods, err := q.SigninMethods(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range methods {
		out.SigninMethods = append(out.SigninMethods, &panelv1.CountBucket{Name: m.Method, Count: m.N})
	}
	providers, err := q.OAuthProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		out.OauthProviders = append(out.OauthProviders, &panelv1.CountBucket{Name: p.Provider, Count: p.N})
	}
	if out.History, err = s.history(ctx, since); err != nil {
		return nil, err
	}
	signups, err := q.SignupsByDay(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	out.SignupsByDay = days(signups)
	active, err := q.ActiveUsersByDay(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, d := range active {
		out.ActiveByDay = append(out.ActiveByDay, &panelv1.CountBucket{Name: d.Day.Time.Format(time.DateOnly), Count: d.N})
	}
	orgs, err := q.LargestOrgs(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		out.LargestOrgs = append(out.LargestOrgs, &panelv1.LargeOrg{Id: id(o.ID), Name: o.Name, Members: o.Members, Nodes: o.Nodes, Servers: o.Servers})
	}
	return out, nil
}

// GetNodeInsights implements StaffService.
func (s *Service) GetNodeInsights(ctx context.Context, req *panelv1.GetNodeInsightsRequest) (*panelv1.GetNodeInsightsResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	c, err := q.NodeInsightCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetNodeInsightsResponse{
		Nodes: c.Nodes, Connected: c.Connected, Cpus: c.Cpus, MemoryBytes: c.MemoryBytes,
		Linked_30D: c.Linked30d, Removed: c.Removed, Servers: c.Servers,
	}
	if out.History, err = s.history(ctx, s.since(req.GetDays())); err != nil {
		return nil, err
	}
	bucket := func(name string, n int32) *panelv1.CountBucket { return &panelv1.CountBucket{Name: name, Count: n} }
	oses, err := q.NodeOSes(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range oses {
		out.Oses = append(out.Oses, bucket(r.Os, r.N))
	}
	archs, err := q.NodeArchs(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range archs {
		out.Archs = append(out.Archs, bucket(r.Arch, r.N))
	}
	versions, err := q.WingsVersions(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range versions {
		out.WingsVersions = append(out.WingsVersions, bucket(r.Version, r.N))
	}
	per, err := q.ServersPerNode(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range per {
		out.ServersPerNode = append(out.ServersPerNode, bucket(r.Bucket, r.N))
	}
	states, err := q.ServerStates(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range states {
		out.ServerStates = append(out.ServerStates, bucket(r.State, r.N))
	}
	eggs, err := q.TopEggs(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range eggs {
		out.TopEggs = append(out.TopEggs, bucket(r.Egg, r.N))
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
	return out, nil
}

// GetBillingInsights implements StaffService. Billing doesn't exist yet,
// so this is Raptor Backup Storage, and what it would bill.
func (s *Service) GetBillingInsights(ctx context.Context, req *panelv1.GetBillingInsightsRequest) (*panelv1.GetBillingInsightsResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	out := &panelv1.GetBillingInsightsResponse{CentsPerTb: hosted.CentsPerTB, IncludedBytes: hosted.IncludedBytesPerNode}
	orgs, err := q.StorageOrgs(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		cents := storage.Estimate(o.Bytes, int(o.Nodes))
		out.StorageOrgs = append(out.StorageOrgs, &panelv1.StorageOrg{Id: id(o.ID), Name: o.Name, Nodes: o.Nodes, Bytes: o.Bytes, CentsMonth: cents})
		out.StorageCentsMonth += cents
		out.StorageBytes += o.Bytes
		out.StorageNodes += o.Nodes
	}
	usage, err := q.StorageUsageByDay(ctx, pgtype.Date{Time: s.since(req.GetDays()), Valid: true})
	if err != nil {
		return nil, err
	}
	for _, u := range usage {
		out.StorageByDay = append(out.StorageByDay, &panelv1.StoragePoint{Day: u.Day.Time.Format(time.DateOnly), Bytes: u.Bytes})
	}

	// Billing: plans, recurring revenue, who's unpaid, and a year of
	// payments by month.
	out.BillingLive = s.billingLive()
	plans, err := q.BillingPlanCounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		out.Plans = append(out.Plans, &panelv1.PlanCount{Status: p.Status, Orgs: p.Orgs, Seats: p.Seats})
		if p.Status == "active" || p.Status == "past_due" {
			out.SeatsBilled += p.Seats
		}
	}
	out.MrrCents = int64(out.SeatsBilled) * billing.NodePriceCents
	unpaid, err := q.BillingUnpaidCounts(ctx)
	if err != nil {
		return nil, err
	}
	out.UnpaidOrgs, out.ReadOnlyOrgs = unpaid.Unpaid, unpaid.ReadOnly
	months, err := q.RevenueByMonth(ctx, pgtype.Timestamptz{Time: time.Now().AddDate(-1, 0, 0), Valid: true})
	if err != nil {
		return nil, err
	}
	for _, m := range months {
		out.RevenueByMonth = append(out.RevenueByMonth, &panelv1.RevenueMonth{Month: m.Month, PaidCents: m.PaidCents, RefundedCents: m.RefundedCents})
	}
	return out, nil
}

// GetInfrastructure implements StaffService: the Panel itself.
func (s *Service) GetInfrastructure(ctx context.Context, _ *panelv1.GetInfrastructureRequest) (*panelv1.GetInfrastructureResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	out := &panelv1.GetInfrastructureResponse{Version: buildinfo.String()}
	instances, err := q.PanelInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range instances {
		out.Instances = append(out.Instances, &panelv1.PanelInstance{Id: i.ID, StartedAt: ts(i.StartedAt), SeenAt: ts(i.SeenAt), Nodes: i.Nodes})
		out.NodesConnected += i.Nodes
	}
	if out.DatabaseBytes, err = q.DatabaseSize(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT c.relname, pg_total_relation_size(c.oid), greatest(c.reltuples, 0)::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY 2 DESC LIMIT 8`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t := &panelv1.TableSize{}
		if err := rows.Scan(&t.Name, &t.Bytes, &t.Rows); err != nil {
			rows.Close()
			return nil, err
		}
		out.LargestTables = append(out.LargestTables, t)
	}
	rows.Close()
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()").Scan(&out.DatabaseConnections); err != nil {
		return nil, err
	}
	sc, err := q.SessionCounts(ctx)
	if err != nil {
		return nil, err
	}
	out.LiveSessions, out.SessionsLastHour = sc.Live, sc.LastHour
	if st, err := (&rollout.Engine{DB: s.DB}).Status(ctx); err == nil {
		out.Rollout = st
	}
	return out, nil
}
