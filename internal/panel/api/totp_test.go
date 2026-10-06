package api

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pquerna/otp/totp"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

var recoveryRe = regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}$`)

func TestTOTP(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	// The clock moves in 30-second steps, so each code is a new one.
	now := time.Now()
	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, DataKey: key, Now: func() time.Time { return now }}
	srv := httptest.NewTLSServer(Handler(Config{Auth: svc, AppOrigin: appOrigin}))
	defer srv.Close()
	tick := func() { now = now.Add(30 * time.Second) }

	emailSignIn := func(b *browser) *panelv1.FinishEmailSignInResponse {
		t.Helper()
		// More sign-ins than an address may ask for in an hour.
		if _, err := db.Exec(ctx, "DELETE FROM rate_events WHERE key LIKE 'send:%'"); err != nil {
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
	second := func(b *browser, proof any) (*panelv1.FinishSecondFactorResponse, error) {
		t.Helper()
		req := &panelv1.FinishSecondFactorRequest{}
		switch p := proof.(type) {
		case totpCode:
			req.Proof = &panelv1.FinishSecondFactorRequest_TotpCode{TotpCode: string(p)}
		case string:
			req.Proof = &panelv1.FinishSecondFactorRequest_RecoveryCode{RecoveryCode: p}
		}
		return b.auth.FinishSecondFactor(ctx, req)
	}

	b := newBrowser(t, srv, appOrigin)
	emailSignIn(b)
	setup, err := b.auth.BeginTOTPSetup(ctx, &panelv1.BeginTOTPSetupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(setup.GetUrl(), "otpauth://totp/Raptor:alice@example.com?") {
		t.Errorf("url: %s", setup.GetUrl())
	}
	code := func() totpCode {
		c, err := totp.GenerateCode(setup.GetSecret(), now)
		if err != nil {
			t.Fatal(err)
		}
		return totpCode(c)
	}
	if _, err := b.auth.FinishTOTPSetup(ctx, &panelv1.FinishTOTPSetupRequest{Code: "000000"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("wrong setup code: %v", err)
	}
	done, err := b.auth.FinishTOTPSetup(ctx, &panelv1.FinishTOTPSetupRequest{Code: string(code())})
	if err != nil {
		t.Fatal(err)
	}
	codes := done.GetRecoveryCodes()
	if len(codes) != auth.RecoveryCodeCount || !recoveryRe.MatchString(codes[0]) || codes[0] == codes[1] {
		t.Fatalf("recovery codes: %v", codes)
	}
	if !strings.Contains(mail.lastMail(t), "now also needs a code from your authenticator app") {
		t.Errorf("no notice: %q", mail.lastMail(t))
	}
	me, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if err != nil || !me.GetUser().GetTotpEnabled() || me.GetRecoveryCodesLeft() != auth.RecoveryCodeCount {
		t.Fatalf("session: %v, %v", me, err)
	}
	if _, err := b.auth.BeginTOTPSetup(ctx, &panelv1.BeginTOTPSetupRequest{}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("second setup: %v", err)
	}

	// Signing in by email now stops halfway.
	b2 := newBrowser(t, srv, appOrigin)
	res := emailSignIn(b2)
	if !res.GetSecondFactorRequired() || res.GetUser() != nil {
		t.Fatalf("email sign-in with TOTP: %v", res)
	}
	if _, err := b2.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("signed in before the second factor: %v", err)
	}
	// The code that turned TOTP on was used up.
	if _, err := second(b2, code()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("setup code reused: %v", err)
	}
	tick()
	used := code()
	if _, err := second(b2, used); err != nil {
		t.Fatal(err)
	}
	me, err = b2.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if err != nil || me.GetSession().GetReauthUntil() == nil {
		t.Fatalf("after the second factor: %v, %v", me, err)
	}
	if _, err := second(b2, used); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("finished twice: %v", err)
	}
	// A code works once, even in another sign-in.
	b3 := newBrowser(t, srv, appOrigin)
	emailSignIn(b3)
	if _, err := second(b3, used); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code replayed: %v", err)
	}
	// A recovery code works once.
	if r, err := second(b3, strings.ToUpper(codes[0])); err != nil || r.GetRecoveryCodesLeft() != auth.RecoveryCodeCount-1 {
		t.Fatalf("recovery code: %v, %v", r, err)
	}
	if !strings.Contains(mail.lastMail(t), "recovery code") {
		t.Errorf("no notice: %q", mail.lastMail(t))
	}
	b4 := newBrowser(t, srv, appOrigin)
	emailSignIn(b4)
	if _, err := second(b4, codes[0]); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("recovery code reused: %v", err)
	}
	// Five wrong tries end the pending sign-in.
	for range auth.PendingAttempts - 1 {
		_, _ = second(b4, totpCode("000000"))
	}
	tick()
	if _, err := second(b4, code()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("after %d wrong tries: %v", auth.PendingAttempts, err)
	}
	// Without the pending sign-in's cookie there's nothing to finish.
	if _, err := second(newBrowser(t, srv, appOrigin), code()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no pending sign-in: %v", err)
	}
	// Pending sign-ins expire.
	b5 := newBrowser(t, srv, appOrigin)
	emailSignIn(b5)
	now = now.Add(auth.PendingSigninTTL + time.Minute)
	if _, err := second(b5, code()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expired pending sign-in: %v", err)
	}

	// Re-authenticating: the app's code, never the inbox.
	tick()
	if _, err := b2.auth.RegenerateRecoveryCodes(ctx, &panelv1.RegenerateRecoveryCodesRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("stale re-auth: %v", err)
	}
	re, err := b2.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{})
	if err != nil || !re.GetTotpAllowed() || re.GetEmailSent() || re.GetPasskey() != nil {
		t.Fatalf("reauth: %v, %v", re, err)
	}
	if _, err := b2.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_EmailCode{EmailCode: "123456"}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("email re-auth with TOTP: %v", err)
	}
	if _, err := b2.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_TotpCode{TotpCode: string(code())}}); err != nil {
		t.Fatal(err)
	}
	fresh, err := b2.auth.RegenerateRecoveryCodes(ctx, &panelv1.RegenerateRecoveryCodesRequest{})
	if err != nil || len(fresh.GetRecoveryCodes()) != auth.RecoveryCodeCount {
		t.Fatalf("regenerate: %v, %v", fresh, err)
	}
	b6 := newBrowser(t, srv, appOrigin)
	emailSignIn(b6)
	if _, err := second(b6, codes[1]); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("old recovery code after regenerating: %v", err)
	}
	if _, err := second(b6, fresh.GetRecoveryCodes()[0]); err != nil {
		t.Errorf("new recovery code: %v", err)
	}

	// Turning it off: email sign-in is one step again.
	if _, err := b2.auth.DisableTOTP(ctx, &panelv1.DisableTOTPRequest{}); err != nil {
		t.Fatal(err)
	}
	if res := emailSignIn(newBrowser(t, srv, appOrigin)); res.GetSecondFactorRequired() || res.GetUser() == nil {
		t.Errorf("sign-in after disabling: %v", res)
	}
	if err := svc.Prune(ctx); err != nil {
		t.Error(err)
	}
}

// Without a data key, TOTP is off rather than storing secrets unencrypted.
func TestTOTPOff(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	srv := httptest.NewTLSServer(Handler(Config{Auth: &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "a@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.auth.BeginTOTPSetup(ctx, &panelv1.BeginTOTPSetupRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("TOTP without a data key: %v", err)
	}
}

type totpCode string
