package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/metric"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
)

// The public demo, Phase 1 (#449, docs/PANEL.md#the-public-demo): anyone
// can start a guest session in the demo org. A guest is a throwaway
// account with no email and no sign-in method, a member of the demo org
// with its showcase servers' guest grants, deleted after GuestTTL. It signs
// in with the normal session cookie, so nothing new reaches nodes: its
// commands are authorized like any member's, plus the demo's deny list.

// GuestTTL is how long a guest lasts.
const GuestTTL = time.Hour

// Limits on guests.
var (
	// limitDemoPerIP is guests one address can start: enough for a few
	// people behind one address, not for a script to fill the demo.
	limitDemoPerIP = Limit{10, time.Hour}
	// maxGuests is how many can be in the demo at once: a bound on rows,
	// not something real traffic is expected to reach.
	maxGuests int64 = 500
)

// GuestDomain is guests' addresses' domain: .invalid is reserved (RFC
// 2606), so nothing is ever delivered there, and the mailer refuses to
// try (mail.Undeliverable).
const GuestDomain = "demo.invalid"

var (
	errDemoClosed   = connect.NewError(connect.CodeFailedPrecondition, errors.New("the demo isn't open right now"))
	errDemoSignedIn = connect.NewError(connect.CodeFailedPrecondition, errors.New("you're signed in to Raptor: sign out first to try the demo"))
	errDemoFull     = connect.NewError(connect.CodeResourceExhausted, errors.New("the demo is full right now; try again in a few minutes"))
	// ErrDemo is what the demo's deny list answers (docs/SECURITY-MODEL.md#the-public-demo).
	ErrDemo = connect.NewError(connect.CodePermissionDenied, errors.New("that's off in the demo: in your own Raptor, you can do it"))
)

// Guest reports whether the session is a demo guest's.
func (s *Session) Guest() bool { return s.User.GuestUntil.Valid }

// GetDemo implements AuthService.
func (s *Service) GetDemo(ctx context.Context, _ *panelv1.GetDemoRequest) (*panelv1.GetDemoResponse, error) {
	_, err := s.q().DemoOrg(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return &panelv1.GetDemoResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &panelv1.GetDemoResponse{Open: true}, nil
}

// StartDemo implements AuthService.
func (s *Service) StartDemo(ctx context.Context, req *panelv1.StartDemoRequest) (*panelv1.StartDemoResponse, error) {
	ip := s.clientIP(ctx)
	if s.Turnstile != nil {
		if err := s.Turnstile.Verify(ctx, req.GetTurnstileToken(), ip.String()); err != nil {
			s.log().Info("turnstile refused", "ip", ip, "err", err)
			return nil, errChallenged
		}
	}
	// Starting a guest session replaces the browser's: a real account's
	// isn't thrown away for one.
	if c, ok := callOf(ctx); ok {
		if sess, err := s.lookup(ctx, cookieToken(c.req)); err == nil && !sess.Guest() && !sess.App() {
			return nil, errDemoSignedIn
		}
	}
	org, err := s.q().DemoOrg(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errDemoClosed
	}
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "demo:ip:"+ip.String(), limitDemoPerIP); err != nil {
		return nil, err
	}
	if n, err := s.q().CountGuests(ctx); err != nil {
		return nil, err
	} else if n >= maxGuests {
		return nil, errDemoFull
	}
	var out *panelv1.StartDemoResponse
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		until := s.now().Add(GuestTTL)
		user, err := q.CreateGuest(ctx, store.CreateGuestParams{
			Email: guestEmail(), GuestUntil: pgtype.Timestamptz{Time: until, Valid: true},
		})
		if err != nil {
			return err
		}
		if err := q.AddDemoMember(ctx, store.AddDemoMemberParams{OrgID: org.ID, UserID: user.ID}); err != nil {
			return err
		}
		servers, err := q.DemoServers(ctx, org.ID)
		if err != nil {
			return err
		}
		for _, d := range servers {
			if err := q.SetServerGrant(ctx, store.SetServerGrantParams{
				OrgID: org.ID, UserID: user.ID, NodeID: d.NodeID, ServerID: d.ServerID, Permissions: d.Permissions,
			}); err != nil {
				return err
			}
		}
		if _, err := s.startSessionUntil(ctx, q, user, false, "demo", until); err != nil {
			return err
		}
		out = &panelv1.StartDemoResponse{OrgId: uuid.UUID(org.ID.Bytes).String(), User: userProto(user)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	demoSessions.Add(ctx, 1)
	return out, nil
}

// demoSessions counts guest sessions started (raptor.demo.sessions), for
// the demo's analytics: a count, nothing about who.
var demoSessions, _ = telemetry.Meter.Int64Counter("raptor.demo.sessions", metric.WithDescription("Guest sessions started in the public demo"))

// guestEmail is a guest's placeholder address: users.email is required and
// unique, and nothing is sent there.
func guestEmail() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "guest-" + hex.EncodeToString(b) + "@" + GuestDomain
}

// Calls a guest may make that change something; everything else they may
// call only reads (NO_SIDE_EFFECTS). What their commands may do is the
// demo's deny list's business (perms.DemoRefuses).
var guestCalls = map[string]bool{
	panelv1connect.AuthServiceSignOutProcedure:               true,
	panelv1connect.CommandServiceExecuteProcedure:            true,
	panelv1connect.OrgServiceTestConnectionProcedure:         true,
	panelv1connect.AlertServiceSeeNotificationsProcedure:     true,
	panelv1connect.AlertServiceDismissNotificationsProcedure: true,
	// Their own sandbox, the egg editor's preview (nothing is saved), and
	// the demo's page counts (Phase 2 and 3).
	panelv1connect.DemoServiceCreateSandboxProcedure:  true,
	panelv1connect.DemoServiceExtendSandboxProcedure:  true,
	panelv1connect.DemoServiceDeleteSandboxProcedure:  true,
	panelv1connect.DemoServicePreviewEggProcedure:     true,
	panelv1connect.DemoServiceRecordDemoPageProcedure: true,
}

// Reads a guest may not make: who else is in the org and how to reach
// them, billing, SFTP and backup keys, and support.
var guestNoReads = map[string]bool{
	panelv1connect.OrgServiceListMembersProcedure:             true,
	panelv1connect.OrgServiceListInvitationsProcedure:         true,
	panelv1connect.OrgServiceListServerAccessProcedure:        true,
	panelv1connect.OrgServiceListMemberPasskeysProcedure:      true,
	panelv1connect.OrgServiceGetSFTPAccessProcedure:           true,
	panelv1connect.OrgServiceGetBackupKeyProcedure:            true,
	panelv1connect.OrgServiceListRecoverySourcesProcedure:     true,
	panelv1connect.OrgServiceGetJoinProgressProcedure:         true,
	panelv1connect.OrgServiceListNodeSupportRequestsProcedure: true,
	panelv1connect.BillingServiceGetBillingProcedure:          true,
	panelv1connect.BillingServiceGetBillingDetailsProcedure:   true,
}

// guestMay refuses a guest's call to anything the demo doesn't offer:
// changing their account (adding an email, a passkey, or a phone), making
// orgs, alert channels, or eggs, and the org's members, billing, and
// support. It's an allow list, so a new RPC is closed to guests until it's
// added. Plain HTTP calls (the live console, file transfers) are commands,
// checked as such.
func guestMay(ctx context.Context) error {
	ci, ok := connect.CallInfoForHandlerContext(ctx)
	if !ok {
		return nil
	}
	return GuestMayCall(ci.Spec().Procedure, ci.Spec().IdempotencyLevel)
}

// GuestMayCall is guestMay's rule: ErrDemo for a procedure a guest may not
// call.
func GuestMayCall(procedure string, idempotency connect.IdempotencyLevel) error {
	if guestCalls[procedure] {
		return nil
	}
	if idempotency == connect.IdempotencyNoSideEffects && !guestNoReads[procedure] {
		return nil
	}
	return ErrDemo
}

// RefuseDemo refuses what's off in the demo (docs/SECURITY-MODEL.md#the-public-demo)
// to everyone but the demo org's owners: a guest, or anyone else who
// isn't an owner of it. It's a no-op outside the demo org, and for people
// who aren't in the org at all (the caller's own checks say no to them).
func RefuseDemo(ctx context.Context, q *store.Queries, sess *Session, org pgtype.UUID) error {
	r, err := q.OrgDemoRole(ctx, store.OrgDemoRoleParams{OrgID: org, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if DemoRestricted(r.Demo, r.Role) {
		return ErrDemo
	}
	return nil
}

// DemoRestricted reports whether someone with role in an org is under the
// demo's deny list: in the demo org, everyone but its owners (staff, who
// set the demo up).
func DemoRestricted(demo bool, role string) bool { return demo && role != "owner" }
