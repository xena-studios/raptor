package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

func TestSignInHardening(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	srv := httptest.NewTLSServer(Handler(Config{Auth: &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)

	// send asks for a code as if from a new IP (the per-IP and per-address
	// send limits are cleared), so only the per-address failure limit is
	// left to stop a distributed guesser.
	send := func() string {
		t.Helper()
		if _, err := db.Exec(ctx, "DELETE FROM rate_events WHERE key NOT LIKE 'fail:%'"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		return code
	}
	try := func(code string) error {
		_, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
		}})
		return err
	}
	wrongFor := func(code string) string {
		if code == "000000" {
			return "111111"
		}
		return "000000"
	}

	// Signing in twice in one browser: the first session ends.
	if err := try(send()); err != nil {
		t.Fatal(err)
	}
	if err := try(send()); err != nil {
		t.Fatal(err)
	}
	list, err := b.auth.ListSessions(ctx, &panelv1.ListSessionsRequest{})
	if err != nil || len(list.GetSessions()) != 1 {
		t.Fatalf("sessions after signing in twice: %v, %v", list, err)
	}

	// 20 wrong codes in a day, over several codes: the address stops taking
	// guesses, even the right code.
	for range 4 {
		code := send()
		for range auth.CodeAttempts {
			if err := try(wrongFor(code)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("wrong code: %v", err)
			}
		}
	}
	if err := try(send()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("right code after 20 wrong ones: %v", err)
	}
}
