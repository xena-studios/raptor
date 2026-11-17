package api

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// pusher records approval requests sent to phones.
type pusher struct {
	mu  sync.Mutex
	got []map[string]string
}

func (p *pusher) push(_ context.Context, _ pgtype.UUID, _, _ string, data map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, data)
	return nil
}

func (p *pusher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.got)
}

func TestPhoneApprovals(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	push := &pusher{}
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, DataKey: key, Now: clock, ApprovalPush: push.push}
	srv := httptest.NewTLSServer(Handler(Config{Auth: svc, AppOrigin: appOrigin}))
	defer srv.Close()

	emailSignIn := func(b *browser) *panelv1.FinishEmailSignInResponse {
		t.Helper()
		if _, err := db.Exec(ctx, "DELETE FROM rate_events"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// Alice, signed in, with the app on her phone; no TOTP yet.
	b := newBrowser(t, srv, appOrigin)
	emailSignIn(b)
	phone := signInPhone(t, srv, b, "Alice's iPhone")
	if got, err := phone.auth.GetPhoneApprover(ctx, &panelv1.GetPhoneApproverRequest{}); err != nil || got.GetCanEnroll() || !strings.Contains(got.GetReason(), "authenticator app") {
		t.Fatalf("without TOTP: %v, %v", got, err)
	}
	if _, err := b.auth.GetPhoneApprover(ctx, &panelv1.GetPhoneApproverRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a browser asking about approving: %v", err)
	}

	// TOTP on.
	setup, err := b.auth.BeginTOTPSetup(ctx, &panelv1.BeginTOTPSetupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(setup.GetSecret(), clock())
	if _, err := b.auth.FinishTOTPSetup(ctx, &panelv1.FinishTOTPSetupRequest{Code: code}); err != nil {
		t.Fatal(err)
	}

	// The phone becomes an approver, once, with a 32-byte secret.
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	if got, err := phone.auth.GetPhoneApprover(ctx, &panelv1.GetPhoneApproverRequest{}); err != nil || !got.GetCanEnroll() {
		t.Fatalf("with TOTP: %v, %v", got, err)
	}
	if _, err := phone.auth.EnrollPhoneApprover(ctx, &panelv1.EnrollPhoneApproverRequest{Secret: secret[:31]}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a short secret: %v", err)
	}
	if _, err := phone.auth.EnrollPhoneApprover(ctx, &panelv1.EnrollPhoneApproverRequest{Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mail.mail[len(mail.mail)-1], "can now approve sign-ins") {
		t.Error("no email about the phone approving")
	}
	if _, err := phone.auth.EnrollPhoneApprover(ctx, &panelv1.EnrollPhoneApproverRequest{Secret: secret}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("enrolling twice: %v", err)
	}
	if _, err := b.auth.EnrollPhoneApprover(ctx, &panelv1.EnrollPhoneApproverRequest{Secret: secret}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a browser enrolling: %v", err)
	}

	// A new browser signs in by email and waits for its second factor.
	b2 := newBrowser(t, srv, appOrigin)
	if res := emailSignIn(b2); !res.GetSecondFactorRequired() {
		t.Fatal("no second factor asked for")
	}
	start := func(br *browser, purpose string) *panelv1.StartPhoneApprovalResponse {
		t.Helper()
		res, err := br.auth.StartPhoneApproval(ctx, &panelv1.StartPhoneApprovalRequest{Purpose: purpose})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	pending := func() *panelv1.PhoneApproval {
		t.Helper()
		list, err := phone.auth.ListPhoneApprovals(ctx, &panelv1.ListPhoneApprovalsRequest{})
		if err != nil || len(list.GetApprovals()) == 0 {
			t.Fatalf("the phone's requests: %v, %v", list, err)
		}
		return list.GetApprovals()[0]
	}
	decide := func(a *panelv1.PhoneApproval, approve bool, number int32, key []byte) (string, error) {
		res, err := phone.auth.DecidePhoneApproval(ctx, &panelv1.DecidePhoneApprovalRequest{
			ApprovalId: a.GetId(), Approve: approve, Number: number,
			Mac: auth.ApprovalMAC(key, a.GetId(), a.GetChallenge(), approve, number),
		})
		return res.GetStatus(), err
	}
	check := func(br *browser, id string) string {
		t.Helper()
		res, err := br.auth.CheckPhoneApproval(ctx, &panelv1.CheckPhoneApprovalRequest{ApprovalId: id})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetStatus()
	}

	req := start(b2, "signin")
	if req.GetNumber() < 10 || req.GetNumber() > 99 || req.GetNotified() != 1 || push.count() != 1 {
		t.Fatalf("the request: %v (pushed %d)", req, push.count())
	}
	if check(b2, req.GetApprovalId()) != "pending" {
		t.Error("not pending")
	}
	a := pending()
	if a.GetId() != req.GetApprovalId() || len(a.GetChoices()) != 3 || !slices.Contains(a.GetChoices(), req.GetNumber()) || a.GetUserAgent() == "" {
		t.Fatalf("what the phone sees: %v", a)
	}

	// Another browser can't see or use it.
	b3 := newBrowser(t, srv, appOrigin)
	emailSignIn(b3)
	if _, err := b3.auth.CheckPhoneApproval(ctx, &panelv1.CheckPhoneApprovalRequest{ApprovalId: req.GetApprovalId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("another browser checking: %v", err)
	}

	// The app's tokens without its secret deny it.
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	if st, err := decide(a, true, req.GetNumber(), other); err != nil || st != "denied" {
		t.Errorf("a wrong secret: %s, %v", st, err)
	}
	if check(b2, req.GetApprovalId()) != "denied" {
		t.Error("still pending after a wrong secret")
	}

	// The wrong number denies it, and Alice is emailed.
	req = start(b2, "signin")
	a = pending()
	wrong := a.GetChoices()[0]
	if wrong == req.GetNumber() {
		wrong = a.GetChoices()[1]
	}
	if st, err := decide(a, true, wrong, secret); err != nil || st != "denied" {
		t.Errorf("the wrong number: %s, %v", st, err)
	}
	if !strings.Contains(mail.mail[len(mail.mail)-1], "wrong number") {
		t.Error("no email about the wrong number")
	}
	if _, err := decide(a, true, req.GetNumber(), secret); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("answering twice: %v", err)
	}

	// The right number signs b2 in, once; b3 can't use it.
	req = start(b2, "signin")
	a = pending()
	if st, err := decide(a, true, req.GetNumber(), secret); err != nil || st != "approved" {
		t.Fatalf("approving: %s, %v", st, err)
	}
	if check(b2, req.GetApprovalId()) != "approved" {
		t.Error("not approved")
	}
	use := func(br *browser) error {
		_, err := br.auth.FinishSecondFactor(ctx, &panelv1.FinishSecondFactorRequest{Proof: &panelv1.FinishSecondFactorRequest_PhoneApprovalId{PhoneApprovalId: req.GetApprovalId()}})
		return err
	}
	if err := use(b3); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("another browser using the approval: %v", err)
	}
	if err := use(b2); err != nil {
		t.Fatal(err)
	}
	if me, err := b2.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); err != nil || me.GetUser().GetEmail() != "alice@example.com" {
		t.Fatalf("signed in by phone: %v, %v", me, err)
	}

	// Requests expire.
	req = start(b3, "signin")
	advance(auth.ApprovalTTL + time.Second)
	if check(b3, req.GetApprovalId()) != "expired" {
		t.Error("not expired")
	}
	if _, err := decide(pendingOrNil(t, ctx, phone, req.GetApprovalId()), true, req.GetNumber(), secret); err == nil {
		t.Error("answered an expired request")
	}

	// Confirming it's Alice, from a signed-in browser.
	advance(auth.ReauthTTL)
	if r, err := b2.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{}); err != nil || !r.GetPhoneAllowed() {
		t.Fatalf("BeginReauth: %v, %v", r, err)
	}
	req = start(b2, "reauth")
	a = pending()
	if a.GetPurpose() != "reauth" {
		t.Errorf("purpose: %s", a.GetPurpose())
	}
	if _, err := decide(a, true, req.GetNumber(), secret); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_PhoneApprovalId{PhoneApprovalId: req.GetApprovalId()}}); err != nil {
		t.Fatalf("FinishReauth: %v", err)
	}

	// Becoming an approver only works right after the phone signs in.
	late := signInPhone(t, srv, b2, "Old phone")
	advance(auth.ApproverEnrollWindow + time.Minute)
	if got, err := late.auth.GetPhoneApprover(ctx, &panelv1.GetPhoneApproverRequest{}); err != nil || got.GetCanEnroll() {
		t.Errorf("long after signing in: %v, %v", got, err)
	}
	if _, err := late.auth.EnrollPhoneApprover(ctx, &panelv1.EnrollPhoneApproverRequest{Secret: secret}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("enrolling late: %v", err)
	}

	// Signing the phone out ends it as an approver.
	me, _ := phone.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if _, err := b2.auth.RevokeSession(ctx, &panelv1.RevokeSessionRequest{Target: &panelv1.RevokeSessionRequest_Id{Id: me.GetSession().GetId()}}); err != nil {
		t.Fatal(err)
	}
	b4 := newBrowser(t, srv, appOrigin)
	emailSignIn(b4)
	if _, err := b4.auth.StartPhoneApproval(ctx, &panelv1.StartPhoneApprovalRequest{Purpose: "signin"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("asking after the phone was signed out: %v", err)
	}
}

// pendingOrNil is the request with this ID as the phone would answer it,
// even when it's no longer listed (expired).
func pendingOrNil(t *testing.T, ctx context.Context, p *phone, id string) *panelv1.PhoneApproval {
	t.Helper()
	list, _ := p.auth.ListPhoneApprovals(ctx, &panelv1.ListPhoneApprovalsRequest{})
	for _, a := range list.GetApprovals() {
		if a.GetId() == id {
			return a
		}
	}
	return &panelv1.PhoneApproval{Id: id, Challenge: make([]byte, 32)}
}
