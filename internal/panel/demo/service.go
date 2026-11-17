package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/store"
	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Service is DemoService: guests' sandboxes, the demo's egg editor
// preview, and the demo's page counts (docs/PANEL.md#sandboxes).
type Service struct {
	Auth      *auth.Service
	Sandboxes *Sandboxes
}

var _ panelv1connect.DemoServiceHandler = (*Service)(nil)

var (
	errNotDemo   = connect.NewError(connect.CodeNotFound, errors.New("no such org"))
	errNoNode    = connect.NewError(connect.CodeFailedPrecondition, errors.New("sandboxes aren't open in the demo right now"))
	errHaveOne   = connect.NewError(connect.CodeAlreadyExists, errors.New("you have a sandbox already: delete it first to try another egg"))
	errSameIP    = connect.NewError(connect.CodeAlreadyExists, errors.New("someone at your address has a sandbox already: one at a time per address"))
	errNoBox     = connect.NewError(connect.CodeNotFound, errors.New("you have no sandbox right now"))
	errNoExtend  = connect.NewError(connect.CodeFailedPrecondition, errors.New("it can't be extended: only once, once it's ready, and only while nobody is waiting"))
	errDoesntFit = connect.NewError(connect.CodeFailedPrecondition, errors.New("that egg is too big for the demo"))
)

// Limits.
const (
	// sandboxesPerHour bounds one guest's sandboxes (each is one at a
	// time anyway).
	sandboxesPerHour = 6
	pagesPerHour     = 2000
)

// caller is who's calling, in which demo org.
type caller struct {
	sess *auth.Session
	org  pgtype.UUID
}

// demoCaller checks the caller is in orgID and it's a demo org: anyone
// else hears there's no such org.
func (s *Service) demoCaller(ctx context.Context, orgID string) (caller, error) {
	var c caller
	id, err := uuid.Parse(orgID)
	if err != nil {
		return c, errNotDemo
	}
	c.org = pgtype.UUID{Bytes: id, Valid: true}
	err = s.Auth.AsUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		c.sess = sess
		r, err := q.OrgDemoRole(ctx, store.OrgDemoRoleParams{OrgID: c.org, UserID: sess.UserID})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !r.Demo) {
			return errNotDemo
		}
		if err != nil {
			return err
		}
		return auth.OrgUsable(ctx, q, c.org)
	})
	return c, err
}

func (s *Service) q() *store.Queries { return store.New(s.Sandboxes.DB) }

// ListDemoEggs implements DemoService.
func (s *Service) ListDemoEggs(ctx context.Context, req *panelv1.ListDemoEggsRequest) (*panelv1.ListDemoEggsResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListDemoEggsResponse{}
	lib, err := s.Sandboxes.library()
	if err != nil {
		return nil, err
	}
	n, err := s.q().DemoNodeOf(ctx, c.org)
	var known []string
	if err == nil {
		out.Arch, out.MaxSandboxes = n.Arch, n.MaxSandboxes
		if st, err := s.Sandboxes.nodeStatus(ctx, uuid.UUID(n.NodeID.Bytes).String(), false); err == nil && demoNodeUp(n.Capabilities) {
			out.Open = true
			out.MaxSandboxes = min(n.MaxSandboxes, int32(st.MaxSandboxes)) //nolint:gosec // at most 32
			known = st.Fingerprints
		}
		live, _ := s.q().LiveSandboxes(ctx, n.NodeID)
		for _, r := range live {
			if r.State == "queued" {
				if !r.Warm {
					out.Queued++
				}
			} else {
				out.Running++
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := s.q().DemoLibraryEggs(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		e, ok := lib[r.CatalogID]
		if !ok || (out.Arch != "" && !slices.Contains(e.Arch, out.Arch)) {
			continue
		}
		d := &panelv1.DemoEgg{EggId: uuid.UUID(r.ID.Bytes).String(), LibraryId: r.CatalogID, Fits: e.Fits, Why: e.Why}
		if d.Fits {
			d.Fits, d.Why = runnable(r.Egg, e.Fingerprint, known)
		}
		out.Eggs = append(out.Eggs, d)
	}
	return out, nil
}

// runnable checks a fitting egg can run on the demo node now: its file is
// still the library's (staff didn't change it), and the node's Wings knows
// it (known nil: the node didn't say).
func runnable(file []byte, fingerprint string, known []string) (bool, string) {
	e, err := eggs.Parse(file)
	if err != nil || eggs.CodeFingerprint(e) != fingerprint {
		return false, "this egg was changed from the library's, so the demo can't run it"
	}
	if known != nil && !slices.Contains(known, fingerprint) {
		return false, "the demo node needs a newer Raptor for this egg"
	}
	return true, ""
}

func demoNodeUp(caps []string) bool { return slices.Contains(caps, nodelink.CapDemo) }

// GetSandbox implements DemoService.
func (s *Service) GetSandbox(ctx context.Context, req *panelv1.GetSandboxRequest) (*panelv1.GetSandboxResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	r, err := s.q().UserSandbox(ctx, store.UserSandboxParams{UserID: c.sess.UserID, OrgID: c.org})
	if errors.Is(err, pgx.ErrNoRows) {
		return &panelv1.GetSandboxResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	sb, err := s.sandbox(ctx, r)
	return &panelv1.GetSandboxResponse{Sandbox: sb}, err
}

// sandbox is a sandbox row as the app shows it.
func (s *Service) sandbox(ctx context.Context, r store.DemoSandbox) (*panelv1.Sandbox, error) {
	out := &panelv1.Sandbox{
		Id: uuid.UUID(r.ID.Bytes).String(), State: r.State, EggId: uuid.UUID(r.EggID.Bytes).String(), EggName: r.EggName,
		LibraryId: r.LibraryID, NodeId: uuid.UUID(r.NodeID.Bytes).String(), ServerId: r.ServerID, Port: r.Port,
		Extended: r.Extended, Error: r.Error,
	}
	if r.ReadyAt.Valid {
		out.ReadyAt = timestamppb.New(r.ReadyAt.Time)
	}
	if r.Deadline.Valid {
		out.Deadline = timestamppb.New(r.Deadline.Time)
	}
	n, err := s.q().DemoNodeOf(ctx, r.OrgID)
	if err != nil {
		return out, nil //nolint:nilerr // the node went: the sandbox did too
	}
	if r.Port != 0 {
		out.Address = nodes.Hostname(n.ShortID, s.Sandboxes.Domain) + ":" + strconv.Itoa(int(r.Port))
	}
	live, err := s.q().LiveSandboxes(ctx, r.NodeID)
	if err != nil {
		return nil, err
	}
	waiting := slices.ContainsFunc(live, func(o store.DemoSandbox) bool { return o.State == "queued" && !o.Warm })
	out.CanExtend = r.State == "ready" && !r.Extended && !waiting
	if r.State == "queued" {
		out.QueuePosition, out.WaitSeconds = queueWait(r, live, int(n.MaxSandboxes), s.Sandboxes.now())
	}
	return out, nil
}

// queueWait is how many guests are ahead of a queued sandbox, and about
// how long until it starts: until as many running sandboxes end as are
// ahead of it, or 15 minutes a round past those.
func queueWait(r store.DemoSandbox, live []store.DemoSandbox, slots int, now time.Time) (ahead, seconds int32) {
	var ends []time.Time
	for _, o := range live {
		switch {
		case o.State == "queued" && !o.Warm && o.QueuedAt.Time.Before(r.QueuedAt.Time):
			ahead++
		case o.State == "ready" && o.Deadline.Valid:
			ends = append(ends, o.Deadline.Time)
		case o.State == "creating" || o.State == "installing":
			// About two minutes to install, then its 15.
			ends = append(ends, now.Add(2*time.Minute+limits.Lifetime))
		}
	}
	slices.SortFunc(ends, time.Time.Compare)
	i := int(ahead)
	var at time.Time
	switch {
	case len(ends) < max(slots, 1):
		at = now // a slot is free; it starts on the next tick
	case i < len(ends):
		at = ends[i]
	default:
		rounds := (i-len(ends))/max(slots, 1) + 1
		at = ends[len(ends)-1].Add(time.Duration(rounds) * limits.Lifetime)
	}
	return ahead, int32(max(0, at.Sub(now).Seconds())) //nolint:gosec // minutes
}

// CreateSandbox implements DemoService.
func (s *Service) CreateSandbox(ctx context.Context, req *panelv1.CreateSandboxRequest) (*panelv1.CreateSandboxResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RateLimit(ctx, "demo:sandbox:"+uuid.UUID(c.sess.UserID.Bytes).String(), sandboxesPerHour, time.Hour); err != nil {
		return nil, err
	}
	n, err := s.q().DemoNodeOf(ctx, c.org)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !demoNodeUp(n.Capabilities)) {
		return nil, errNoNode
	}
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetEggId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("pick an egg"))
	}
	egg, err := s.q().DemoLibraryEgg(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("that egg isn't in Raptor's library"))
	}
	if err != nil {
		return nil, err
	}
	lib, err := s.Sandboxes.library()
	if err != nil {
		return nil, err
	}
	e, ok := lib[egg.CatalogID]
	if !ok || !e.Fits || (n.Arch != "" && !slices.Contains(e.Arch, n.Arch)) {
		return nil, errDoesntFit
	}
	if ok, why := runnable(egg.Egg, e.Fingerprint, nil); !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(why))
	}
	if parsed, err := eggs.Parse(egg.Egg); err == nil && slices.Contains(parsed.Features, "eula") && !req.GetAcceptEula() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("accept the game's EULA to make this server"))
	}
	r, err := s.q().QueueSandbox(ctx, store.QueueSandboxParams{
		OrgID: c.org, NodeID: n.NodeID, UserID: c.sess.UserID, IpHash: s.Sandboxes.ipHash(s.Auth.ClientIP(ctx).String()),
		EggID: egg.ID, LibraryID: egg.CatalogID, EggName: egg.Name, AcceptEula: req.GetAcceptEula(),
	})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		if pg.ConstraintName == "demo_sandboxes_ip" {
			return nil, errSameIP
		}
		return nil, errHaveOne
	}
	if err != nil {
		return nil, err
	}
	sb, err := s.sandbox(ctx, r)
	return &panelv1.CreateSandboxResponse{Sandbox: sb}, err
}

// live returns the caller's sandbox that's queued or running.
func (s *Service) live(ctx context.Context, c caller) (store.DemoSandbox, error) {
	r, err := s.q().UserSandbox(ctx, store.UserSandboxParams{UserID: c.sess.UserID, OrgID: c.org})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (r.State == "gone" || r.State == "failed")) {
		return r, errNoBox
	}
	return r, err
}

// ExtendSandbox implements DemoService.
func (s *Service) ExtendSandbox(ctx context.Context, req *panelv1.ExtendSandboxRequest) (*panelv1.ExtendSandboxResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	r, err := s.live(ctx, c)
	if err != nil {
		return nil, err
	}
	sb, err := s.sandbox(ctx, r)
	if err != nil {
		return nil, err
	}
	if !sb.GetCanExtend() {
		return nil, errNoExtend
	}
	// Wings allows it once per sandbox, whatever the Panel says.
	raw, err := s.Sandboxes.send(ctx, uuid.UUID(r.NodeID.Bytes).String(), uuid.UUID(c.sess.UserID.Bytes).String(), "node.demo.extend", r.ServerID, nil, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the demo node said no: %w", err))
	}
	var b nodeSandbox
	if err := json.Unmarshal(raw, &b); err == nil && b.Deadline != 0 {
		_ = s.q().SandboxReady(ctx, store.SandboxReadyParams{
			ID: r.ID, ReadyAt: ts(time.Unix(b.ReadyAt, 0)), Deadline: ts(time.Unix(b.Deadline, 0)), Extended: true,
		})
		r.Deadline, r.Extended = ts(time.Unix(b.Deadline, 0)), true
	}
	sb, err = s.sandbox(ctx, r)
	return &panelv1.ExtendSandboxResponse{Sandbox: sb}, err
}

// DeleteSandbox implements DemoService.
func (s *Service) DeleteSandbox(ctx context.Context, req *panelv1.DeleteSandboxRequest) (*panelv1.DeleteSandboxResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	r, err := s.live(ctx, c)
	if err != nil {
		return nil, err
	}
	if r.ServerID != "" {
		// Signed with the demo key, which the demo node takes for its own
		// sandboxes and nothing else.
		if _, err := s.Sandboxes.send(ctx, uuid.UUID(r.NodeID.Bytes).String(), uuid.UUID(c.sess.UserID.Bytes).String(), "server.delete", r.ServerID, nil, true); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("the demo node didn't delete it (it will when its time is up): %w", err))
		}
	} else if r.State == "creating" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("it's being made right now; delete it in a moment"))
	}
	s.Sandboxes.end(ctx, s.q(), r, "gone", "")
	return &panelv1.DeleteSandboxResponse{}, nil
}

// PreviewEgg implements DemoService.
func (s *Service) PreviewEgg(ctx context.Context, req *panelv1.PreviewEggRequest) (*panelv1.PreviewEggResponse, error) {
	c, err := s.demoCaller(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RateLimit(ctx, "demo:preview:"+uuid.UUID(c.sess.UserID.Bytes).String(), 120, time.Hour); err != nil {
		return nil, err
	}
	rev, sp, err := orgs.PreviewEgg(req.GetFile())
	if err != nil {
		return nil, err
	}
	return &panelv1.PreviewEggResponse{Review: rev, Spec: sp}, nil
}

// pagePattern is a route of the app: letters, slashes, and $params, so no
// ID (or anything else about the guest) can be in it.
var pagePattern = regexp.MustCompile(`^/[a-zA-Z$_./-]{0,120}$`)

// RecordDemoPage implements DemoService. Only guests' pages are counted.
func (s *Service) RecordDemoPage(ctx context.Context, req *panelv1.RecordDemoPageRequest) (*panelv1.RecordDemoPageResponse, error) {
	sess, err := s.Auth.Current(ctx)
	if err != nil {
		return nil, err
	}
	if !sess.Guest() {
		return &panelv1.RecordDemoPageResponse{}, nil
	}
	if !pagePattern.MatchString(req.GetPage()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a page is an app route"))
	}
	if err := s.Auth.RateLimit(ctx, "demo:pages:"+uuid.UUID(sess.UserID.Bytes).String(), pagesPerHour, time.Hour); err != nil {
		return nil, err
	}
	pagesReached.Add(ctx, 1, metric.WithAttributes(attribute.String("page", req.GetPage())))
	return &panelv1.RecordDemoPageResponse{}, nil
}
