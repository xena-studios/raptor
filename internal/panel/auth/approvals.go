package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/mail"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Phone approvals (docs/PANEL.md#phone-approvals): instead of typing an
// authenticator app's code, the person picks, on Raptor's phone app, the
// number the browser shows. Only accounts with TOTP on can use them, so the
// authenticator app and recovery codes stay the fallback.
//
// An approver is an app session holding a secret: 32 bytes the app made and
// keeps in the phone's Keychain or Keystore behind Face ID, which the Panel
// keeps sealed. An answer is an HMAC with it over the request's challenge,
// so the app's tokens alone (copied off the phone) can't approve anything.
// A session can only become an approver within 15 minutes of signing in,
// which needed a fresh confirmation in the browser, so a token copied later
// can't enroll its own secret either. Number matching stops approvals by
// reflex (an attacker sending request after request).

const (
	// ApprovalTTL is how long a request waits for the phone.
	ApprovalTTL = 2 * time.Minute
	// ApproverEnrollWindow is how soon after the app signs in it can
	// become an approver.
	ApproverEnrollWindow = 15 * time.Minute
)

var (
	limitApprovalsPerUser = Limit{10, time.Hour}

	errAppOnly       = connect.NewError(connect.CodeFailedPrecondition, errors.New("only Raptor's phone app can do this"))
	errNoApprover    = connect.NewError(connect.CodeFailedPrecondition, errors.New("no phone approves sign-ins for this account"))
	errApprovalGone  = connect.NewError(connect.CodeNotFound, errors.New("that request expired or was already answered"))
	errNotApprover   = connect.NewError(connect.CodePermissionDenied, errors.New("this phone doesn't approve sign-ins: turn it on in the app's settings"))
	errBadApprovalID = connect.NewError(connect.CodeInvalidArgument, errors.New("bad approval ID"))
)

// ApprovalPusher sends a notification to the phone app signed in as
// session (the alerts service's PushToSession; nil: none are sent, and
// requests only show when the app is opened).
type ApprovalPusher func(ctx context.Context, session pgtype.UUID, title, body string, data map[string]string) error

func approverAAD(user pgtype.UUID) []byte {
	return append([]byte("raptor approver v1\x00"), user.Bytes[:]...)
}

func (s *Service) sealApprover(user pgtype.UUID, secret []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(append([]byte{1}, nonce...), nonce, secret, approverAAD(user)), nil
}

func (s *Service) openApprover(user pgtype.UUID, sealed []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(sealed) < 1+a.NonceSize() || sealed[0] != 1 {
		return nil, errors.New("unknown approver secret format")
	}
	n := 1 + a.NonceSize()
	return a.Open(nil, sealed[1:n], sealed[n:], approverAAD(user))
}

// ApprovalMAC is what the phone sends to answer a request.
func ApprovalMAC(secret []byte, approvalID string, challenge []byte, approve bool, number int32) []byte {
	decision := "deny"
	if approve {
		decision = "approve"
	}
	m := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(m, "raptor approval v1\n%s\n%s\n%s\n%d", approvalID, base64.RawURLEncoding.EncodeToString(challenge), decision, number)
	return m.Sum(nil)
}

func randInt(n int) int {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n)) //nolint:gosec // n is small and positive
}

// approvalNumbers is the number to pick and three choices with it, in a
// random order.
func approvalNumbers() (int32, []int32) {
	n := int32(10 + randInt(90)) //nolint:gosec // 10..99
	choices := []int32{n}
	for len(choices) < 3 {
		c := int32(10 + randInt(90)) //nolint:gosec // 10..99
		dup := false
		for _, x := range choices {
			dup = dup || x == c
		}
		if !dup {
			choices = append(choices, c)
		}
	}
	for i := len(choices) - 1; i > 0; i-- {
		j := randInt(i + 1)
		choices[i], choices[j] = choices[j], choices[i]
	}
	return n, choices
}

// describeAgent turns a user agent into "Chrome on macOS", as the web app's
// list of devices does.
type agentPattern struct {
	re   *regexp.Regexp
	name string
}

var (
	browsers = []agentPattern{
		{regexp.MustCompile(`Edg/`), "Edge"},
		{regexp.MustCompile(`OPR/`), "Opera"},
		{regexp.MustCompile(`Firefox/`), "Firefox"},
		{regexp.MustCompile(`Chrome/`), "Chrome"},
		{regexp.MustCompile(`Safari/`), "Safari"},
	}
	systems = []agentPattern{
		{regexp.MustCompile(`iPhone|iPad`), "iOS"},
		{regexp.MustCompile(`Android`), "Android"},
		{regexp.MustCompile(`Mac OS X|Macintosh`), "macOS"},
		{regexp.MustCompile(`Windows`), "Windows"},
		{regexp.MustCompile(`Linux`), "Linux"},
	}
)

func firstMatch(ps []agentPattern, ua string) string {
	for _, p := range ps {
		if p.re.MatchString(ua) {
			return p.name
		}
	}
	return ""
}

func describeAgent(ua string) string {
	browser, os := firstMatch(browsers, ua), firstMatch(systems, ua)
	switch {
	case browser != "" && os != "":
		return browser + " on " + os
	case browser != "" || os != "":
		return browser + os
	}
	return "a browser"
}

// cannotEnroll is why an app session can't become an approver now (empty
// if it can).
func (s *Service) cannotEnroll(sess *Session) string {
	if !sess.User.TotpEnabledAt.Valid {
		return "Turn on an authenticator app first, in the web app's security settings: approving from this phone stands in for its codes."
	}
	if s.now().Sub(sess.CreatedAt.Time) > ApproverEnrollWindow {
		return "This can only be turned on right after this phone signs in. Sign out here and sign in again to set it up."
	}
	return ""
}

func (s *Service) appSession(ctx context.Context) (*Session, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if !sess.App() {
		return nil, errAppOnly
	}
	return sess, nil
}

// GetPhoneApprover implements AuthService.
func (s *Service) GetPhoneApprover(ctx context.Context, _ *panelv1.GetPhoneApproverRequest) (*panelv1.GetPhoneApproverResponse, error) {
	sess, err := s.appSession(ctx)
	if err != nil {
		return nil, err
	}
	_, err = s.q().PhoneApprover(ctx, sess.ID)
	if err == nil {
		return &panelv1.GetPhoneApproverResponse{Enrolled: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	reason := s.cannotEnroll(sess)
	return &panelv1.GetPhoneApproverResponse{CanEnroll: reason == "", Reason: reason}, nil
}

// EnrollPhoneApprover implements AuthService.
func (s *Service) EnrollPhoneApprover(ctx context.Context, req *panelv1.EnrollPhoneApproverRequest) (*panelv1.EnrollPhoneApproverResponse, error) {
	sess, err := s.appSession(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.GetSecret()) != 32 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the secret must be 32 bytes"))
	}
	if reason := s.cannotEnroll(sess); reason != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(reason))
	}
	sealed, err := s.sealApprover(sess.UserID, req.GetSecret())
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.PhoneApprover(ctx, sess.ID); err == nil {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("this phone already approves sign-ins"))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := q.AddPhoneApprover(ctx, store.AddPhoneApproverParams{SessionID: sess.ID, UserID: sess.UserID, Secret: sealed}); err != nil {
			return err
		}
		return s.Audit(ctx, q, Event{User: sess.UserID, Action: "approver.add", Target: uuid.UUID(sess.ID.Bytes).String(), Meta: map[string]any{"device": sess.DeviceName}})
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, mail.Email{
		Subject: "Your phone can approve sign-ins to Raptor",
		Title:   "Sign-ins can be approved from your phone",
		Body:    []string{"Raptor's app on your phone can now approve sign-ins and confirm it's you, instead of a code from your authenticator app."},
		Facts:   []mail.Fact{{Label: "Device", Value: sess.DeviceName, Mono: true}},
		Button:  s.securityLink(),
		Note:    "If this wasn't you, sign that phone out in your security settings right away.",
	})
	return &panelv1.EnrollPhoneApproverResponse{}, nil
}

// RemovePhoneApprover implements AuthService.
func (s *Service) RemovePhoneApprover(ctx context.Context, _ *panelv1.RemovePhoneApproverRequest) (*panelv1.RemovePhoneApproverResponse, error) {
	sess, err := s.appSession(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.q().RemovePhoneApprover(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		_ = s.Audit(ctx, nil, Event{User: sess.UserID, Action: "approver.remove", Target: uuid.UUID(sess.ID.Bytes).String(), Meta: map[string]any{"device": sess.DeviceName}})
	}
	return &panelv1.RemovePhoneApproverResponse{}, nil
}

// phoneAllowed reports whether the user can approve from a phone: TOTP on,
// and a phone that approves.
func (s *Service) phoneAllowed(ctx context.Context, q *store.Queries, user store.User) (bool, error) {
	if !user.TotpEnabledAt.Valid {
		return false, nil
	}
	n, err := q.CountPhoneApprovers(ctx, user.ID)
	return n > 0, err
}

// StartPhoneApproval implements AuthService.
func (s *Service) StartPhoneApproval(ctx context.Context, req *panelv1.StartPhoneApprovalRequest) (*panelv1.StartPhoneApprovalResponse, error) {
	q := s.q()
	params := store.CreatePhoneApprovalParams{Purpose: req.GetPurpose()}
	var user store.User
	switch req.GetPurpose() {
	case "signin":
		p, err := s.pendingSignin(ctx, q)
		if err != nil {
			return nil, err
		}
		params.PendingSigninID = p.ID
		if user, err = q.GetUser(ctx, p.UserID); err != nil {
			return nil, err
		}
	case "reauth":
		sess, err := s.Current(ctx)
		if err != nil {
			return nil, err
		}
		if err := RefuseApp(sess); err != nil {
			return nil, err
		}
		params.SessionID = sess.ID
		user = sess.User
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(`purpose is "signin" or "reauth"`))
	}
	if ok, err := s.phoneAllowed(ctx, q, user); err != nil {
		return nil, err
	} else if !ok {
		return nil, errNoApprover
	}
	if err := s.limit(ctx, "approval:user:"+uuid.UUID(user.ID.Bytes).String(), limitApprovalsPerUser); err != nil {
		return nil, err
	}
	sessions, err := q.PhoneApproverSessions(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, errNoApprover
	}
	ip, ua := s.callerDetails(ctx)
	params.UserID, params.Ip, params.UserAgent = user.ID, ip, ua
	params.Number, params.Choices = approvalNumbers()
	params.Challenge = make([]byte, 32)
	_, _ = rand.Read(params.Challenge)
	params.ExpiresAt = pgtype.Timestamptz{Time: s.now().Add(ApprovalTTL), Valid: true}
	var ap store.PhoneApproval
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.CancelPendingApprovals(ctx, store.CancelPendingApprovalsParams{PendingSigninID: params.PendingSigninID, SessionID: params.SessionID}); err != nil {
			return err
		}
		var err error
		ap, err = q.CreatePhoneApproval(ctx, params)
		return err
	})
	if err != nil {
		return nil, err
	}
	id := uuid.UUID(ap.ID.Bytes).String()
	where := describeAgent(ua)
	if ip != nil {
		where += ", " + ip.String()
	}
	title := "Is this you signing in?"
	if params.Purpose == "reauth" {
		title = "Is this you confirming it's you?"
	}
	notified := int32(0)
	if s.ApprovalPush != nil {
		for _, sid := range sessions {
			if err := s.ApprovalPush(context.WithoutCancel(ctx), sid, title, where+". Open Raptor to approve or deny.", map[string]string{"kind": "approval", "approval_id": id}); err != nil {
				s.log().Info("sending an approval request failed", "err", err)
				continue
			}
			notified++
		}
	}
	return &panelv1.StartPhoneApprovalResponse{ApprovalId: id, Number: ap.Number, ExpiresAt: timestamppb.New(ap.ExpiresAt.Time), Notified: notified}, nil
}

// pendingSignin is the browser's sign-in waiting for its second factor.
func (s *Service) pendingSignin(ctx context.Context, q *store.Queries) (store.PendingSignin, error) {
	c, ok := callOf(ctx)
	if !ok {
		return store.PendingSignin{}, errNoPending
	}
	token := namedCookie(c.req, pendingCookie)
	if token == "" {
		return store.PendingSignin{}, errNoPending
	}
	p, err := q.PendingSigninByToken(ctx, hash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errNoPending
	}
	if err != nil {
		return p, err
	}
	if p.Attempts >= PendingAttempts || !p.ExpiresAt.Time.After(s.now()) {
		return p, errNoPending
	}
	return p, nil
}

func parseApprovalID(id string) (pgtype.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, errBadApprovalID
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

// CheckPhoneApproval implements AuthService.
func (s *Service) CheckPhoneApproval(ctx context.Context, req *panelv1.CheckPhoneApprovalRequest) (*panelv1.CheckPhoneApprovalResponse, error) {
	id, err := parseApprovalID(req.GetApprovalId())
	if err != nil {
		return nil, err
	}
	q := s.q()
	ap, err := q.PhoneApproval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errApprovalGone
	}
	if err != nil {
		return nil, err
	}
	// Only the browser that asked.
	switch ap.Purpose {
	case "signin":
		p, err := s.pendingSignin(ctx, q)
		if err != nil {
			return nil, err
		}
		if p.ID != ap.PendingSigninID {
			return nil, errApprovalGone
		}
	case "reauth":
		sess, err := s.Current(ctx)
		if err != nil {
			return nil, err
		}
		if sess.ID != ap.SessionID {
			return nil, errApprovalGone
		}
	}
	status := ap.Status
	if status == "pending" && !ap.ExpiresAt.Time.After(s.now()) {
		status = "expired"
	}
	return &panelv1.CheckPhoneApprovalResponse{Status: status}, nil
}

// ListPhoneApprovals implements AuthService.
func (s *Service) ListPhoneApprovals(ctx context.Context, _ *panelv1.ListPhoneApprovalsRequest) (*panelv1.ListPhoneApprovalsResponse, error) {
	sess, err := s.appSession(ctx)
	if err != nil {
		return nil, err
	}
	q := s.q()
	if _, err := q.PhoneApprover(ctx, sess.ID); errors.Is(err, pgx.ErrNoRows) {
		return &panelv1.ListPhoneApprovalsResponse{}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := q.PendingPhoneApprovals(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListPhoneApprovalsResponse{}
	for _, r := range rows {
		a := &panelv1.PhoneApproval{
			Id: uuid.UUID(r.ID.Bytes).String(), Purpose: r.Purpose, Choices: r.Choices, Challenge: r.Challenge,
			UserAgent: describeAgent(r.UserAgent), CreatedAt: timestamppb.New(r.CreatedAt.Time), ExpiresAt: timestamppb.New(r.ExpiresAt.Time),
		}
		if r.Ip != nil {
			a.Ip = r.Ip.String()
		}
		out.Approvals = append(out.Approvals, a)
	}
	return out, nil
}

// DecidePhoneApproval implements AuthService.
func (s *Service) DecidePhoneApproval(ctx context.Context, req *panelv1.DecidePhoneApprovalRequest) (*panelv1.DecidePhoneApprovalResponse, error) {
	sess, err := s.appSession(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseApprovalID(req.GetApprovalId())
	if err != nil {
		return nil, err
	}
	var status, reason string
	var ap store.PhoneApproval
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		approver, err := q.PhoneApprover(ctx, sess.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotApprover
		}
		if err != nil {
			return err
		}
		ap, err = q.LockPhoneApproval(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && ap.UserID != sess.UserID) {
			return errApprovalGone
		}
		if err != nil {
			return err
		}
		if ap.Status != "pending" || !ap.ExpiresAt.Time.After(s.now()) {
			return errApprovalGone
		}
		secret, err := s.openApprover(sess.UserID, approver.Secret)
		if err != nil {
			return err
		}
		want := ApprovalMAC(secret, req.GetApprovalId(), ap.Challenge, req.GetApprove(), req.GetNumber())
		switch {
		case !hmac.Equal(want, req.GetMac()):
			// The app's tokens without its secret: someone else has them.
			status, reason = "denied", "bad_proof"
		case !req.GetApprove():
			status, reason = "denied", "denied"
		case req.GetNumber() != ap.Number:
			status, reason = "denied", "wrong_number"
		default:
			status = "approved"
		}
		if err := q.DecidePhoneApproval(ctx, store.DecidePhoneApprovalParams{ID: ap.ID, Status: status}); err != nil {
			return err
		}
		if err := q.TouchPhoneApprover(ctx, sess.ID); err != nil {
			return err
		}
		ev := Event{
			User: sess.UserID, Action: "approval.approve", Target: req.GetApprovalId(),
			Meta: map[string]any{"purpose": ap.Purpose, "device": sess.DeviceName},
		}
		if status == "denied" {
			ev.Action = "approval.deny"
			ev.Meta["reason"] = reason
		}
		return s.Audit(ctx, q, ev)
	})
	if err != nil {
		return nil, err
	}
	if status == "denied" {
		facts := []mail.Fact{{Label: "From", Value: describeAgent(ap.UserAgent), Mono: true}}
		if ap.Ip != nil {
			facts = append(facts, mail.Fact{Label: "Address", Value: ap.Ip.String(), Mono: true})
		}
		body := "A sign-in to your Raptor account asked your phone for approval, and it was denied."
		if reason == "wrong_number" {
			body = "A sign-in to your Raptor account asked your phone for approval, and the wrong number was picked, so it was denied."
		}
		s.notify(ctx, sess.User, mail.Email{
			Subject: "A sign-in to your Raptor account was denied",
			Title:   "A sign-in was denied",
			Body:    []string{body},
			Facts:   facts,
			Button:  s.securityLink(),
			Note:    "If this wasn't you, someone knows how to get past your first sign-in step (your email, or your Google, GitHub, or Discord account): secure that, and sign out devices you don't know.",
		})
	}
	return &panelv1.DecidePhoneApprovalResponse{Status: status}, nil
}

// usePhoneApproval checks an approval the phone approved for this sign-in
// or session, and uses it up.
func usePhoneApproval(ctx context.Context, q *store.Queries, id string, match func(store.PhoneApproval) bool) (bool, error) {
	aid, err := parseApprovalID(id)
	if err != nil {
		return false, nil //nolint:nilerr // a bad ID is a failed proof
	}
	ap, err := q.LockPhoneApproval(ctx, aid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !match(ap) || ap.Status != "approved" {
		return false, nil
	}
	n, err := q.UsePhoneApproval(ctx, aid)
	return n == 1, err
}
