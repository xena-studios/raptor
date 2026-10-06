package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
)

const fuzzOrigin = "https://app.example.test"

// fuzzPasskey is a deterministic passkey and its account: the fuzzer runs
// in several processes, so keys and challenges must be the same in each.
func fuzzPasskey(f *testing.F) (*webauthn.WebAuthn, *commandtest.Authenticator, *waUser) {
	wa, err := NewWebAuthn(fuzzOrigin)
	if err != nil {
		f.Fatal(err)
	}
	a, err := commandtest.New("EdDSA", fuzzOrigin, "app.example.test", make([]byte, 32))
	if err != nil {
		f.Fatal(err)
	}
	u := &waUser{User: store.User{Email: "a@example.com", WebauthnHandle: bytes.Repeat([]byte{7}, 32)}}
	u.creds = []webauthn.Credential{{
		ID: a.CredentialID, PublicKey: a.COSE,
		Flags: webauthn.CredentialFlags{UserPresent: true, UserVerified: true},
	}}
	return wa, a, u
}

var b64url = base64.RawURLEncoding

// Passkey sign-in answers: nothing crashes the parser or the checks, and
// nothing verifies unless it decodes to exactly what the passkey signed.
func FuzzPasskeySignIn(f *testing.F) {
	wa, a, u := fuzzPasskey(f)
	challenge := bytes.Repeat([]byte{1}, 32)
	ad, cd, sig := a.Assert(challenge)
	good, _ := json.Marshal(map[string]any{
		"id": b64url.EncodeToString(a.CredentialID), "rawId": b64url.EncodeToString(a.CredentialID), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64url.EncodeToString(ad), "clientDataJSON": b64url.EncodeToString(cd),
			"signature": b64url.EncodeToString(sig), "userHandle": b64url.EncodeToString(u.WebauthnHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
	find := func(rawID, handle []byte) (webauthn.User, error) {
		if !bytes.Equal(rawID, a.CredentialID) || !bytes.Equal(handle, u.WebauthnHandle) {
			return nil, errBadPasskey
		}
		return u, nil
	}
	verify := func(answer []byte) (*protocol.ParsedCredentialAssertionData, error) {
		parsed, err := protocol.ParseCredentialRequestResponseBytes(answer)
		if err != nil {
			return nil, err
		}
		_, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired), webauthn.WithChallenge(challenge))
		if err != nil {
			return nil, err
		}
		_, _, err = wa.ValidatePasskeyLogin(find, *data, parsed)
		return parsed, err
	}
	// The seed must verify, or the fuzzer only ever explores failures.
	if _, err := verify(good); err != nil {
		f.Fatalf("the good answer doesn't verify: %v", err)
	}
	f.Add(good)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"id":"","rawId":"","type":"public-key","response":{}}`))
	f.Fuzz(func(t *testing.T, answer []byte) {
		if len(answer) > maxCredentialJSON {
			return
		}
		parsed, err := verify(answer)
		if err != nil {
			return
		}
		r := parsed.Raw.AssertionResponse
		if !bytes.Equal(r.AuthenticatorData, ad) || !bytes.Equal(r.ClientDataJSON, cd) || !bytes.Equal(r.Signature, sig) {
			t.Fatalf("a changed answer verified: %s", answer)
		}
	})
}

// Registration answers: nothing crashes, and nothing is saved that didn't
// verify the user.
func FuzzPasskeyRegistration(f *testing.F) {
	wa, a, u := fuzzPasskey(f)
	challenge := bytes.Repeat([]byte{2}, 32)
	att, cd := a.Register(challenge)
	good, _ := json.Marshal(map[string]any{
		"id": b64url.EncodeToString(a.CredentialID), "rawId": b64url.EncodeToString(a.CredentialID), "type": "public-key",
		"response":               map[string]any{"attestationObject": b64url.EncodeToString(att), "clientDataJSON": b64url.EncodeToString(cd)},
		"clientExtensionResults": map[string]any{},
	})
	newUser := &waUser{User: u.User}
	register := func(answer []byte) (*webauthn.Credential, *protocol.ParsedCredentialCreationData, error) {
		parsed, err := protocol.ParseCredentialCreationResponseBytes(answer)
		if err != nil {
			return nil, nil, err
		}
		_, data, err := wa.BeginRegistration(newUser,
			webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
				ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
				UserVerification: protocol.VerificationRequired,
			}),
			webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		)
		if err != nil {
			return nil, nil, err
		}
		data.Challenge = b64url.EncodeToString(challenge)
		cred, err := wa.CreateCredential(newUser, *data, parsed)
		return cred, parsed, err
	}
	if _, _, err := register(good); err != nil {
		f.Fatalf("the good answer doesn't register: %v", err)
	}
	f.Add(good)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, answer []byte) {
		if len(answer) > maxCredentialJSON {
			return
		}
		cred, parsed, err := register(answer)
		if err != nil {
			return
		}
		if !cred.Flags.UserVerified || !bytes.Equal(cred.ID, parsed.RawID) {
			t.Fatalf("saved a passkey that didn't verify the user, or under another ID: %+v", cred)
		}
	})
}

// TOTP codes: only the code for a step within one of now matches, spacing
// aside, and a step at or before the last used one never does.
func FuzzMatchTOTP(f *testing.F) {
	const secret = "JBSWY3DPEHPK3PXP"
	f.Add("123456", int64(1_800_000_000), int64(0))
	f.Add(" 12 34 56 ", int64(0), int64(0))
	f.Add("", int64(-1), int64(-1))
	f.Fuzz(func(t *testing.T, code string, unix, last int64) {
		if unix < 0 || unix > 1<<40 {
			return
		}
		now := time.Unix(unix, 0)
		step, ok := matchTOTP(secret, code, now, last)
		if !ok {
			return
		}
		cur := unix / totpPeriod
		if step < cur-1 || step > cur+1 || step <= last {
			t.Fatalf("matched step %d at %d (last %d)", step, cur, last)
		}
		want, _ := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if strings.ReplaceAll(strings.TrimSpace(code), " ", "") != want {
			t.Fatalf("%q matched step %d, whose code is %s", code, step, want)
		}
	})
}

// Email addresses: normalizing is idempotent, and what comes out is
// lowercase with no spaces or characters that could break a header.
func FuzzNormalizeEmail(f *testing.F) {
	for _, s := range []string{"Alice@Example.com", " a@b.c ", "a@b", "@b.c", "a\r\nBcc: x@y.z@b.c", "ä@ö.de"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e, err := normalizeEmail(s)
		if err != nil {
			return
		}
		if e2, err := normalizeEmail(e); err != nil || e2 != e {
			t.Fatalf("not idempotent: %q -> %q -> %q, %v", s, e, e2, err)
		}
		if e != strings.ToLower(e) || strings.ContainsFunc(e, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			t.Fatalf("%q normalized to %q", s, e)
		}
	})
}

// Recovery codes: however they're typed, the same code hashes the same.
func FuzzRecoveryCode(f *testing.F) {
	f.Add("abcd-efgh-ijkl-mnop")
	f.Add(" ABCD EFGH IJKL MNOP ")
	f.Fuzz(func(t *testing.T, c string) {
		n := normalizeRecoveryCode(c)
		if normalizeRecoveryCode(n) != n {
			t.Fatalf("not idempotent: %q", c)
		}
		user := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
		if !bytes.Equal(recoveryHash(user, c), recoveryHash(user, n)) {
			t.Fatalf("%q and %q hash differently", c, n)
		}
	})
}
