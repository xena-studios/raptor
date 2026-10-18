package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Event is an audit log entry (docs/PANEL.md#audit-log).
type Event struct {
	// Org is set for org events; account events have none.
	Org pgtype.UUID
	// User is the account it's about.
	User pgtype.UUID
	// Actor did it (User if unset).
	Actor  pgtype.UUID
	Action string
	Target string
	Meta   map[string]any
	// Staff: Raptor's staff did it (Actor is the staff member).
	Staff bool
}

// Audit records an event with the request's IP and browser, in q (the
// caller's transaction, so the event and the change commit together) or,
// with q nil, on its own.
func (s *Service) Audit(ctx context.Context, q *store.Queries, ev Event) error {
	own := q == nil
	if own {
		q = s.q()
	}
	meta := []byte("{}")
	if len(ev.Meta) > 0 {
		var err error
		if meta, err = json.Marshal(ev.Meta); err != nil {
			return err
		}
	}
	if !ev.Actor.Valid {
		ev.Actor = ev.User
	}
	var ip *netip.Addr
	if a := s.clientIP(ctx); a.IsValid() {
		ip = &a
	}
	ua := ""
	if c, ok := callOf(ctx); ok {
		ua = truncate(c.req.Get("User-Agent"), 256)
	}
	actor := "user"
	if ev.Staff {
		actor = "staff"
	}
	err := q.AddAuditEvent(ctx, store.AddAuditEventParams{
		OrgID: ev.Org, UserID: ev.User, Actor: actor, ActorID: ev.Actor, Action: ev.Action, Target: ev.Target,
		Ip: ip, UserAgent: ua, Metadata: meta,
	})
	if err != nil && own {
		// Nothing to roll back: say so and carry on.
		s.log().Error("writing the audit log failed", "action", ev.Action, "err", err)
		return nil
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// AuditProto is an audit log row for the API.
func AuditProto(id pgtype.UUID, at pgtype.Timestamptz, actor, actorEmail, subjectEmail, action, target string, ip *netip.Addr, ua string, meta []byte) *panelv1.AuditEvent {
	out := &panelv1.AuditEvent{
		Id: uuid.UUID(id.Bytes).String(), At: timestamppb.New(at.Time), Action: action, Actor: actor, ActorEmail: actorEmail,
		Target: target, UserAgent: ua, MetadataJson: string(meta), SubjectEmail: subjectEmail,
	}
	if ip != nil {
		out.Ip = ip.String()
	}
	return out
}

// PageSize is how many audit events a page has.
const PageSize = 50

// PageToken reads a page token (an event ID; empty for the first page).
func PageToken(t string) (pgtype.UUID, error) {
	if t == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid.Parse(t)
	if err != nil {
		return pgtype.UUID{}, errors.New("bad page token")
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// Filter is an ActivityFilter as the audit queries take it: unset parts
// are NULL or empty, so they don't narrow anything.
type Filter struct {
	From, To pgtype.Timestamptz
	IP       pgtype.Text
	Actor    pgtype.UUID
	Actions  []string
	Failed   bool
	Query    pgtype.Text
}

// actionPattern is an action, or a prefix of one ending in % (LIKE's
// wildcard, the only one allowed).
var actionPattern = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)*\.?%?$`)

// likeEscaper escapes LIKE's wildcards (and its escape character), so
// what's searched for is matched as typed.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ParseFilter checks an activity filter and makes it a Filter.
func ParseFilter(f *panelv1.ActivityFilter) (Filter, error) {
	// Never nil: a nil slice is NULL to Postgres, which matches nothing.
	out := Filter{Actions: []string{}}
	if f == nil {
		return out, nil
	}
	if q := strings.TrimSpace(f.GetQuery()); q != "" {
		if utf8.RuneCountInString(q) > 100 {
			return out, errors.New("the search is too long: use up to 100 characters")
		}
		out.Query = pgtype.Text{String: "%" + likeEscaper.Replace(q) + "%", Valid: true}
	}
	if t := f.GetFrom(); t != nil {
		out.From = pgtype.Timestamptz{Time: t.AsTime(), Valid: true}
	}
	if t := f.GetTo(); t != nil {
		out.To = pgtype.Timestamptz{Time: t.AsTime(), Valid: true}
	}
	if out.From.Valid && out.To.Valid && !out.To.Time.After(out.From.Time) {
		return out, errors.New("the end of the date range is before its start")
	}
	if ip := strings.TrimSpace(f.GetIp()); ip != "" {
		p, err := netip.ParsePrefix(ip)
		if err != nil {
			a, aerr := netip.ParseAddr(ip)
			if aerr != nil {
				return out, errors.New("that isn't an IP address or range")
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out.IP = pgtype.Text{String: p.Masked().String(), Valid: true}
	}
	if id := f.GetActorId(); id != "" {
		u, err := uuid.Parse(id)
		if err != nil {
			return out, errors.New("bad person to filter by")
		}
		out.Actor = pgtype.UUID{Bytes: u, Valid: true}
	}
	if len(f.GetActions()) > 30 {
		return out, errors.New("too many kinds of event: pick up to 30")
	}
	for _, a := range f.GetActions() {
		if !actionPattern.MatchString(a) {
			return out, errors.New("bad kind of event to filter by")
		}
		out.Actions = append(out.Actions, a)
	}
	out.Failed = f.GetFailed()
	return out, nil
}

// ListActivity implements AuthService.
func (s *Service) ListActivity(ctx context.Context, req *panelv1.ListActivityRequest) (*panelv1.ListActivityResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	before, err := PageToken(req.GetPageToken())
	if err != nil {
		return nil, invalid(err)
	}
	f, err := ParseFilter(req.GetFilter())
	if err != nil {
		return nil, invalid(err)
	}
	rows, err := s.q().UserActivity(ctx, store.UserActivityParams{
		UserID: sess.UserID, Before: before, FromAt: f.From, ToAt: f.To, Ip: f.IP,
		Actions: f.Actions, Failed: f.Failed, Query: f.Query, Lim: PageSize,
	})
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListActivityResponse{}
	for _, r := range rows {
		out.Events = append(out.Events, AuditProto(r.ID, r.At, r.Actor, "", "", r.Action, r.Target, r.Ip, r.UserAgent, r.Metadata))
	}
	if len(rows) == PageSize {
		out.NextPageToken = uuid.UUID(rows[len(rows)-1].ID.Bytes).String()
	}
	return out, nil
}

func invalid(err error) error { return connect.NewError(connect.CodeInvalidArgument, err) }
