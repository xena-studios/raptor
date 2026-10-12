package staff

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Announcements (docs/PANEL.md#announcements): plain text banners staff
// publish to everyone signed in, between a start and an end.

func announcement(a store.Announcement) *panelv1.Announcement {
	return &panelv1.Announcement{
		Id: id(a.ID), Level: a.Level, Title: a.Title, Body: a.Body, Link: a.Link,
		StartsAt: ts(a.StartsAt), EndsAt: ts(a.EndsAt), CreatedBy: a.CreatedBy, CreatedAt: ts(a.CreatedAt),
	}
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }

// checkAnnouncement validates and normalizes what staff wrote.
func checkAnnouncement(a *panelv1.Announcement, now time.Time) (level, title, body, link string, starts, ends pgtype.Timestamptz, err error) {
	level = a.GetLevel()
	if level != "info" && level != "warning" && level != "maintenance" {
		return "", "", "", "", starts, ends, invalid("the level is info, warning, or maintenance")
	}
	title = strings.TrimSpace(a.GetTitle())
	if title == "" || utf8.RuneCountInString(title) > 120 {
		return "", "", "", "", starts, ends, invalid("the title is 1 to 120 characters")
	}
	body = strings.TrimSpace(a.GetBody())
	if utf8.RuneCountInString(body) > 1000 {
		return "", "", "", "", starts, ends, invalid("the text is at most 1,000 characters")
	}
	link = strings.TrimSpace(a.GetLink())
	if link != "" {
		u, perr := url.Parse(link)
		if perr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(link) > 500 {
			return "", "", "", "", starts, ends, invalid("the link is an https:// address")
		}
	}
	starts = pgtype.Timestamptz{Time: now, Valid: true}
	if a.GetStartsAt() != nil {
		starts.Time = a.GetStartsAt().AsTime()
	}
	if a.GetEndsAt() != nil {
		ends = pgtype.Timestamptz{Time: a.GetEndsAt().AsTime(), Valid: true}
		if !ends.Time.After(starts.Time) {
			return "", "", "", "", starts, ends, invalid("it has to end after it starts")
		}
	}
	return level, title, body, link, starts, ends, nil
}

// ListActiveAnnouncements implements StaffService.
func (s *Service) ListActiveAnnouncements(ctx context.Context, _ *panelv1.ListActiveAnnouncementsRequest) (*panelv1.ListActiveAnnouncementsResponse, error) {
	if _, err := s.Auth.Current(ctx); err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).ActiveAnnouncements(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListActiveAnnouncementsResponse{}
	for _, r := range rows {
		out.Announcements = append(out.Announcements, announcement(r))
	}
	return out, nil
}

// ListAnnouncements implements StaffService.
func (s *Service) ListAnnouncements(ctx context.Context, _ *panelv1.ListAnnouncementsRequest) (*panelv1.ListAnnouncementsResponse, error) {
	if _, err := s.staff(ctx, false); err != nil {
		return nil, err
	}
	rows, err := store.New(s.DB).ListAnnouncements(ctx)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListAnnouncementsResponse{}
	for _, r := range rows {
		out.Announcements = append(out.Announcements, announcement(r))
	}
	return out, nil
}

// SaveAnnouncement implements StaffService.
func (s *Service) SaveAnnouncement(ctx context.Context, req *panelv1.SaveAnnouncementRequest) (*panelv1.SaveAnnouncementResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	a := req.GetAnnouncement()
	level, title, body, link, starts, ends, err := checkAnnouncement(a, s.Auth.Clock())
	if err != nil {
		return nil, err
	}
	q := store.New(s.DB)
	meta := map[string]any{"level": level, "title": title}
	if a.GetId() == "" {
		row, err := q.CreateAnnouncement(ctx, store.CreateAnnouncementParams{
			Level: level, Title: title, Body: body, Link: link, StartsAt: starts, EndsAt: ends, CreatedBy: c.sess.User.Email,
		})
		if err != nil {
			return nil, err
		}
		_ = s.audit(ctx, c, "announcement.create", "announcement", id(row.ID), meta)
		return &panelv1.SaveAnnouncementResponse{Id: id(row.ID)}, nil
	}
	aid, err := parseID(a.GetId(), "announcement")
	if err != nil {
		return nil, err
	}
	n, err := q.UpdateAnnouncement(ctx, store.UpdateAnnouncementParams{
		ID: aid, Level: level, Title: title, Body: body, Link: link, StartsAt: starts, EndsAt: ends,
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errNotFound
	}
	_ = s.audit(ctx, c, "announcement.update", "announcement", id(aid), meta)
	return &panelv1.SaveAnnouncementResponse{Id: id(aid)}, nil
}

// EndAnnouncement implements StaffService.
func (s *Service) EndAnnouncement(ctx context.Context, req *panelv1.EndAnnouncementRequest) (*panelv1.EndAnnouncementResponse, error) {
	c, err := s.staff(ctx, true)
	if err != nil {
		return nil, err
	}
	aid, err := parseID(req.GetId(), "announcement")
	if err != nil {
		return nil, err
	}
	if _, err := store.New(s.DB).EndAnnouncement(ctx, aid); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, c, "announcement.end", "announcement", id(aid), nil)
	return &panelv1.EndAnnouncementResponse{}, nil
}
