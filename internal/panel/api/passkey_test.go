package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
)

// passkey is a software passkey for the test app's origin.
type passkey struct {
	*commandtest.Authenticator
	handle []byte // the user handle it was registered with
}

func newPasskey(t *testing.T) *passkey {
	t.Helper()
	a, err := commandtest.New("ES256", appOrigin, "app.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &passkey{Authenticator: a}
}

var b64 = base64.RawURLEncoding

// options reads the challenge (and user handle, when registering) from a
// ceremony's options.
func options(t *testing.T, ch *panelv1.PasskeyChallenge) (challenge, handle []byte) {
	t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(ch.GetOptionsJson()), &o); err != nil {
		t.Fatal(err)
	}
	challenge, err := b64.DecodeString(o.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	if o.User.ID != "" {
		if handle, err = b64.DecodeString(o.User.ID); err != nil {
			t.Fatal(err)
		}
	}
	return challenge, handle
}

// create answers a registration like navigator.credentials.create().toJSON().
func (p *passkey) create(t *testing.T, ch *panelv1.PasskeyChallenge) *panelv1.PasskeyAnswer {
	t.Helper()
	challenge, handle := options(t, ch)
	p.handle = handle
	att, cd := p.Register(challenge)
	j, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(p.CredentialID), "rawId": b64.EncodeToString(p.CredentialID), "type": "public-key",
		"response":               map[string]any{"attestationObject": b64.EncodeToString(att), "clientDataJSON": b64.EncodeToString(cd), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	})
	return &panelv1.PasskeyAnswer{CeremonyId: ch.GetCeremonyId(), CredentialJson: string(j)}
}

// get answers a sign-in like navigator.credentials.get().toJSON().
func (p *passkey) get(t *testing.T, ch *panelv1.PasskeyChallenge) *panelv1.PasskeyAnswer {
	t.Helper()
	challenge, _ := options(t, ch)
	ad, cd, sig := p.Assert(challenge)
	j, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(p.CredentialID), "rawId": b64.EncodeToString(p.CredentialID), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64.EncodeToString(ad), "clientDataJSON": b64.EncodeToString(cd),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(p.handle),
		},
		"clientExtensionResults": map[string]any{},
	})
	return &panelv1.PasskeyAnswer{CeremonyId: ch.GetCeremonyId(), CredentialJson: string(j)}
}

// lastMail returns the newest email.
func (b *inbox) lastMail(t *testing.T) string {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.mail) == 0 {
		t.Fatal("no email")
	}
	return b.mail[len(b.mail)-1]
}

func TestPasskeys(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	wa, err := auth.NewWebAuthn(appOrigin)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	svc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, WebAuthn: wa, Now: func() time.Time {
		if now.IsZero() {
			return time.Now()
		}
		return now
	}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: svc, AppOrigin: appOrigin}))
	defer srv.Close()

	emailSignIn := func(b *browser, email string) *panelv1.User {
		t.Helper()
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetUser()
	}
	reauthUntil := func(b *browser) time.Time {
		t.Helper()
		s, err := b.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if s.GetSession().GetReauthUntil() == nil {
			return time.Time{}
		}
		return s.GetSession().GetReauthUntil().AsTime()
	}
	register := func(b *browser, p *passkey, name string) (*panelv1.FinishPasskeyRegistrationResponse, error) {
		t.Helper()
		begin, err := b.auth.BeginPasskeyRegistration(ctx, &panelv1.BeginPasskeyRegistrationRequest{})
		if err != nil {
			return nil, err
		}
		return b.auth.FinishPasskeyRegistration(ctx, &panelv1.FinishPasskeyRegistrationRequest{Answer: p.create(t, begin.GetChallenge()), Name: name})
	}
	signIn := func(b *browser, p *passkey) (*panelv1.FinishPasskeySignInResponse, error) {
		t.Helper()
		begin, err := b.auth.BeginPasskeySignIn(ctx, &panelv1.BeginPasskeySignInRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return b.auth.FinishPasskeySignIn(ctx, &panelv1.FinishPasskeySignInRequest{Answer: p.get(t, begin.GetChallenge())})
	}

	// A new account: signing up by email counts as re-authenticating, since
	// the inbox is all it has, so it can add a passkey straight away.
	b := newBrowser(t, srv, appOrigin)
	alice := emailSignIn(b, "alice@example.com")
	if reauthUntil(b).IsZero() {
		t.Fatal("a new account isn't re-authenticated")
	}
	laptop := newPasskey(t)
	added, err := register(b, laptop, "  Laptop ")
	if err != nil {
		t.Fatal(err)
	}
	if added.GetPasskey().GetName() != "Laptop" || !strings.Contains(mail.lastMail(t), `"Laptop" was added`) {
		t.Errorf("added: %v; mail: %q", added, mail.lastMail(t))
	}
	if _, err := register(b, laptop, "Again"); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("the same passkey twice: %v", err)
	}

	// Signing in with it, on another device, without an email.
	b2 := newBrowser(t, srv, appOrigin)
	res, err := signIn(b2, laptop)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetUser().GetId() != alice.GetId() || reauthUntil(b2).IsZero() {
		t.Errorf("passkey sign-in: %v", res)
	}
	// An answer works once.
	begin, err := b2.auth.BeginPasskeySignIn(ctx, &panelv1.BeginPasskeySignInRequest{})
	if err != nil {
		t.Fatal(err)
	}
	answer := laptop.get(t, begin.GetChallenge())
	if _, err := b2.auth.FinishPasskeySignIn(ctx, &panelv1.FinishPasskeySignInRequest{Answer: answer}); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.auth.FinishPasskeySignIn(ctx, &panelv1.FinishPasskeySignInRequest{Answer: answer}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("replayed answer: %v", err)
	}

	// Answers that must not work.
	refused := func(what string, change func(p *passkey)) {
		t.Helper()
		bad := &passkey{Authenticator: new(commandtest.Authenticator), handle: laptop.handle}
		*bad.Authenticator = *laptop.Authenticator
		change(bad)
		if _, err := signIn(newBrowser(t, srv, appOrigin), bad); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: %v", what, err)
		}
	}
	refused("phishing origin", func(p *passkey) { p.Origin = "https://app.example.test.evil.test" })
	refused("sibling origin", func(p *passkey) { p.Origin = "https://www.example.test" })
	refused("registrable domain as RP ID", func(p *passkey) { p.RPID = "example.test" })
	refused("no user verification", func(p *passkey) { p.Flags = commandtest.FlagUserPresent })
	refused("registration instead of sign-in", func(p *passkey) { p.Type = "webauthn.create" })
	refused("cross-origin frame", func(p *passkey) { p.CrossOrigin = true })
	refused("another account's handle", func(p *passkey) { p.handle = []byte("someone else") })
	refused("unknown passkey", func(p *passkey) {
		other := newPasskey(t)
		p.Authenticator = other.Authenticator
	})
	// A counter going backwards means a copied key.
	laptop.Counter = 10
	if _, err := signIn(newBrowser(t, srv, appOrigin), laptop); err != nil {
		t.Fatal(err)
	}
	refused("counter went backwards", func(p *passkey) { p.Counter = 5 })

	// Signing in by email to an account with a passkey isn't enough to
	// change its sign-in methods: someone in the inbox could otherwise
	// remove the passkeys.
	b3 := newBrowser(t, srv, appOrigin)
	emailSignIn(b3, "alice@example.com")
	if !reauthUntil(b3).IsZero() {
		t.Error("email sign-in re-authenticated an account with a passkey")
	}
	list, err := b3.auth.ListPasskeys(ctx, &panelv1.ListPasskeysRequest{})
	if err != nil || len(list.GetPasskeys()) != 1 || list.GetPasskeys()[0].GetLastUsedAt() == nil {
		t.Fatalf("list: %v, %v", list, err)
	}
	id := list.GetPasskeys()[0].GetId()
	if _, err := b3.auth.BeginPasskeyRegistration(ctx, &panelv1.BeginPasskeyRegistrationRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("registration without re-auth: %v", err)
	}
	if _, err := b3.auth.DeletePasskey(ctx, &panelv1.DeletePasskeyRequest{Id: id}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("delete without re-auth: %v", err)
	}
	// Renaming is only a label.
	if _, err := b3.auth.RenamePasskey(ctx, &panelv1.RenamePasskeyRequest{Id: id, Name: "Work laptop"}); err != nil {
		t.Error(err)
	}
	if _, err := b3.auth.RenamePasskey(ctx, &panelv1.RenamePasskeyRequest{Id: id, Name: strings.Repeat("x", 65)}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("long name: %v", err)
	}
	// Re-authenticating has to use the passkey, not the inbox.
	re, err := b3.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{})
	if err != nil || re.GetPasskey() == nil {
		t.Fatalf("reauth: %v, %v", re, err)
	}
	if _, err := b3.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_EmailCode{EmailCode: "123456"}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("email re-auth with a passkey: %v", err)
	}
	// A ceremony belongs to the session that started it, even another of
	// the same user's.
	laptop.Counter++
	if _, err := b2.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_Passkey{Passkey: laptop.get(t, re.GetPasskey())}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("another session's ceremony: %v", err)
	}
	re, err = b3.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	laptop.Counter++
	if _, err := b3.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_Passkey{Passkey: laptop.get(t, re.GetPasskey())}}); err != nil {
		t.Fatal(err)
	}
	if reauthUntil(b3).IsZero() {
		t.Fatal("not re-authenticated")
	}
	// It lasts 5 minutes.
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	if _, err := b3.auth.DeletePasskey(ctx, &panelv1.DeletePasskeyRequest{Id: id}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("stale re-auth: %v", err)
	}
	now = time.Time{}

	// Someone else can't touch it.
	bob := newBrowser(t, srv, appOrigin)
	emailSignIn(bob, "bob@example.com")
	if _, err := bob.auth.RenamePasskey(ctx, &panelv1.RenamePasskeyRequest{Id: id, Name: "mine"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("someone else's passkey renamed: %v", err)
	}
	if _, err := bob.auth.DeletePasskey(ctx, &panelv1.DeletePasskeyRequest{Id: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("someone else's passkey deleted: %v", err)
	}
	if _, err := b3.auth.DeletePasskey(ctx, &panelv1.DeletePasskeyRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mail.lastMail(t), `"Work laptop" was removed`) {
		t.Errorf("no notice: %q", mail.lastMail(t))
	}
	if _, err := signIn(newBrowser(t, srv, appOrigin), laptop); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("deleted passkey signed in: %v", err)
	}

	// Without passkeys, re-authenticating is by an emailed code, which
	// can't be used to sign in.
	now = time.Now().Add(auth.ReauthTTL + time.Minute)
	re, err = b3.auth.BeginReauth(ctx, &panelv1.BeginReauthRequest{})
	if err != nil || !re.GetEmailSent() {
		t.Fatalf("email reauth: %v, %v", re, err)
	}
	code := codeRe.FindStringSubmatch(mail.lastMail(t))[1]
	if strings.Contains(mail.lastMail(t), "/signin/link#") {
		t.Error("a re-auth email has a sign-in link")
	}
	if _, err := newBrowser(t, srv, appOrigin).auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
	}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("re-auth code signed in: %v", err)
	}
	if _, err := b3.auth.FinishReauth(ctx, &panelv1.FinishReauthRequest{Proof: &panelv1.FinishReauthRequest_EmailCode{EmailCode: code}}); err != nil {
		t.Fatal(err)
	}
	now = time.Time{}
	if _, err := register(b3, newPasskey(t), "Phone"); err != nil {
		t.Errorf("register after email re-auth: %v", err)
	}
	if err := svc.Prune(ctx); err != nil {
		t.Error(err)
	}
}

// Without a relying party, passkeys are off rather than broken.
func TestPasskeysOff(t *testing.T) {
	db := paneltest.NewDB(t)
	srv := httptest.NewTLSServer(Handler(Config{Auth: &auth.Service{DB: db, AppURL: appOrigin}, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.BeginPasskeySignIn(context.Background(), &panelv1.BeginPasskeySignInRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("passkeys off: %v", err)
	}
}
