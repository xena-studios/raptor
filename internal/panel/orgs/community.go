package orgs

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Community eggs (docs/PANEL.md#community-eggs): an org shares a published
// egg with everyone on Raptor. Others add it to their own eggs after
// reviewing what it runs (CustomizeEgg with as_is), report it, and Raptor's
// staff can take it out of the community.

const (
	communityPage   = 30
	reportsPerHour  = 10
	maxReportDetail = 1000
	maxRemoveReason = 500
)

var reportReasons = []string{"malware", "broken", "spam", "other"}

// ListCommunityEggs implements EggService.
func (s *Eggs) ListCommunityEggs(ctx context.Context, req *panelv1.ListCommunityEggsRequest) (*panelv1.ListCommunityEggsResponse, error) {
	var l *lib
	if err := s.tx(ctx, req.GetOrgId(), false, func(lb *lib) error { l = lb; return nil }); err != nil {
		return nil, err
	}
	off, _ := strconv.Atoi(req.GetPageToken())
	category := req.GetCategory()
	if category != "" && !slices.Contains(Categories, category) {
		return nil, invalid(errors.New("choose one of the categories"))
	}
	query := strings.TrimSpace(req.GetQuery())
	if utf8.RuneCountInString(query) > 100 {
		query = string([]rune(query)[:100])
	}
	// The publishers' names are other orgs': read on the Panel's own
	// connection, which the query filters itself.
	rows, err := store.New(s.Orgs.DB).ListCommunityEggs(ctx, store.ListCommunityEggsParams{
		IncludeRemoved: l.official, Query: query, Category: category, Newest: req.GetSort() == "new",
		Off: int32(max(off, 0)), Lim: communityPage + 1, //nolint:gosec // a page offset
	})
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListCommunityEggsResponse{}
	if len(rows) > communityPage {
		rows = rows[:communityPage]
		out.NextPageToken = strconv.Itoa(max(off, 0) + communityPage)
	}
	for _, r := range rows {
		p, err := describeEgg(l, r.Egg, "")
		if err != nil {
			continue
		}
		p.Publisher, p.Copies = r.OrgName, r.Copies
		out.Eggs = append(out.Eggs, p)
	}
	return out, nil
}

// ShareEgg implements EggService.
func (s *Eggs) ShareEgg(ctx context.Context, req *panelv1.ShareEggRequest) (*panelv1.ShareEggResponse, error) {
	if req.GetOrgId() == "" {
		return nil, invalid(errors.New("official eggs are offered to everyone already"))
	}
	err := s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, err := editable(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if req.GetShared() {
			if row.RemovedAt.Valid {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("Raptor's staff took this egg out of the community: "+row.RemovedReason))
			}
			if row.Status != "published" {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("publish the egg before sharing it"))
			}
		}
		if row.SharedAt.Valid == req.GetShared() {
			return nil
		}
		if _, err := l.q.SetEggShared(ctx, store.SetEggSharedParams{ID: row.ID, Shared: req.GetShared()}); err != nil {
			return err
		}
		action := "egg.unshare"
		if req.GetShared() {
			action = "egg.share"
		}
		return s.audit(ctx, l, action, idString(row.ID), map[string]any{"name": row.Name})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.ShareEggResponse{}, nil
}

// ReportEgg implements EggService.
func (s *Eggs) ReportEgg(ctx context.Context, req *panelv1.ReportEggRequest) (*panelv1.ReportEggResponse, error) {
	if !slices.Contains(reportReasons, req.GetReason()) {
		return nil, invalid(errors.New("say what's wrong: malware, broken, spam, or other"))
	}
	details := strings.TrimSpace(req.GetDetails())
	if utf8.RuneCountInString(details) > maxReportDetail {
		return nil, invalid(errors.New("the details are at most 1,000 characters"))
	}
	if req.GetReason() == "other" && details == "" {
		return nil, invalid(errors.New("say what's wrong"))
	}
	var egg, user pgtype.UUID
	err := s.tx(ctx, req.GetOrgId(), false, func(l *lib) error {
		if l.official {
			return errNotOfficer
		}
		row, _, err := s.load(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if !row.SharedAt.Valid || l.owns(*row) {
			return invalid(errors.New("only eggs other orgs share can be reported"))
		}
		egg, user = row.ID, l.userID()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.Orgs.Auth.RateLimit(ctx, "eggreport:user:"+idString(user), reportsPerHour, time.Hour); err != nil {
		return nil, err
	}
	n, err := store.New(s.Orgs.DB).AddEggReport(ctx, store.AddEggReportParams{
		EggID: egg, ReporterID: user, Reason: req.GetReason(), Details: details,
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("you've reported this egg already; Raptor's staff will look at it"))
	}
	return &panelv1.ReportEggResponse{}, nil
}

// ListEggReports implements EggService.
func (s *Eggs) ListEggReports(ctx context.Context, _ *panelv1.ListEggReportsRequest) (*panelv1.ListEggReportsResponse, error) {
	if s.Staff == nil {
		return nil, errNotOfficer
	}
	if _, err := s.Staff.Require(ctx, false); err != nil {
		return nil, err
	}
	rows, err := store.New(s.Orgs.DB).ListOpenEggReports(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListEggReportsResponse{}
	for _, r := range rows {
		out.Reports = append(out.Reports, &panelv1.EggReport{
			Id: idString(r.ID), EggId: idString(r.EggID), EggName: r.EggName, Publisher: r.OrgName, Reason: r.Reason,
			Details: r.Details, ReporterEmail: r.ReporterEmail, CreatedAt: ts(r.CreatedAt), Removed: r.RemovedAt.Valid,
		})
	}
	return out, nil
}

// ModerateEgg implements EggService.
func (s *Eggs) ModerateEgg(ctx context.Context, req *panelv1.ModerateEggRequest) (*panelv1.ModerateEggResponse, error) {
	if s.Staff == nil {
		return nil, errNotOfficer
	}
	a, err := s.Staff.Require(ctx, true)
	if err != nil {
		return nil, err
	}
	id, ok := eggID(req.GetId())
	if !ok {
		return nil, errNoEgg
	}
	q := store.New(s.Orgs.DB)
	row, err := q.GetEgg(ctx, id)
	if err != nil || !row.Egg.SharedAt.Valid && !row.Egg.RemovedAt.Valid {
		return nil, errNoEgg
	}
	reason := strings.TrimSpace(req.GetReason())
	meta := map[string]any{"name": row.Egg.Name}
	switch req.GetAction() {
	case "remove":
		if reason == "" || utf8.RuneCountInString(reason) > maxRemoveReason {
			return nil, invalid(errors.New("say why, in up to 500 characters: the egg's org sees it"))
		}
		if _, err := q.RemoveCommunityEgg(ctx, store.RemoveCommunityEggParams{ID: id, Reason: reason, RemovedBy: a.Email()}); err != nil {
			return nil, err
		}
		if _, err := q.ResolveEggReports(ctx, store.ResolveEggReportsParams{EggID: id, ResolvedBy: a.Email(), Resolution: "removed"}); err != nil {
			return nil, err
		}
		meta["reason"] = reason
	case "restore":
		if _, err := q.RestoreCommunityEgg(ctx, id); err != nil {
			return nil, err
		}
	case "dismiss":
		if _, err := q.ResolveEggReports(ctx, store.ResolveEggReportsParams{EggID: id, ResolvedBy: a.Email(), Resolution: "dismissed"}); err != nil {
			return nil, err
		}
	default:
		return nil, invalid(errors.New("the action is remove, restore, or dismiss"))
	}
	_ = s.Staff.Record(ctx, a, "egg."+req.GetAction(), "egg", idString(id), meta)
	if req.GetAction() != "dismiss" {
		// The org sees it in its own log too.
		_ = s.Orgs.Auth.Audit(ctx, nil, auth.Event{
			Org: row.Egg.OrgID, Actor: a.UserID(), Staff: true, Action: "egg.community." + req.GetAction(),
			Target: idString(id), Meta: meta,
		})
	}
	return &panelv1.ModerateEggResponse{}, nil
}
