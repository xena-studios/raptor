package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Passkey limits.
const (
	CeremonyTTL    = 5 * time.Minute
	MaxPasskeys    = 20
	maxPasskeyName = 64
	// maxCredentialJSON bounds what the browser sends back; real answers
	// are a few kilobytes.
	maxCredentialJSON = 64 << 10
)

// NewWebAuthn is the relying party for the web app at appURL: the RP ID is
// its hostname (app.raptorpanel.net, never the registrable domain, so other
// subdomains can't ask for signatures; docs/DECISIONS.md #82), and only
// that origin may answer.
func NewWebAuthn(appURL string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(appURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("app URL %q has no hostname", appURL)
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Raptor",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
		},
	})
}

// waUser is an account as go-webauthn sees it.
type waUser struct {
	store.User
	creds []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte   { return u.WebauthnHandle }
func (u *waUser) WebAuthnName() string { return u.Email }
func (u *waUser) WebAuthnDisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadWAUser loads a user's passkeys. ids maps credential IDs to rows, for
// updating the one that answers.
func loadWAUser(ctx context.Context, q *store.Queries, user store.User) (*waUser, map[string]store.Passkey, error) {
	rows, err := q.ListPasskeys(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	u := &waUser{User: user}
	ids := map[string]store.Passkey{}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return nil, nil, fmt.Errorf("passkey %x: %w", r.ID.Bytes, err)
		}
		u.creds = append(u.creds, c)
		ids[string(r.CredentialID)] = r
	}
	return u, ids, nil
}

// hasStrongMethod says whether the account has a sign-in method stronger
// than its inbox (a passkey; TOTP once it exists).
func hasStrongMethod(ctx context.Context, q *store.Queries, user pgtype.UUID) (bool, error) {
	n, err := q.CountPasskeys(ctx, user)
	return n > 0, err
}

func (s *Service) webAuthn() (*webauthn.WebAuthn, error) {
	if s.WebAuthn == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("passkeys aren't set up on this Panel"))
	}
	return s.WebAuthn, nil
}

// saveCeremony stores a ceremony's session data and returns the challenge
// for the browser.
func (s *Service) saveCeremony(ctx context.Context, purpose string, session pgtype.UUID, data *webauthn.SessionData, options any) (*panelv1.PasskeyChallenge, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	opts, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	id, err := s.q().CreateCeremony(ctx, store.CreateCeremonyParams{
		Purpose: purpose, SessionID: session, Data: raw,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(CeremonyTTL), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.PasskeyChallenge{CeremonyId: uuid.UUID(id.Bytes).String(), OptionsJson: string(opts)}, nil
}

// takeCeremony uses up a ceremony, checking it's for purpose and was
// started by session (unset for signing in).
func (s *Service) takeCeremony(ctx context.Context, q *store.Queries, id, purpose string, session pgtype.UUID) (*webauthn.SessionData, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return nil, errBadPasskey
	}
	row, err := q.TakeCeremony(ctx, pgtype.UUID{Bytes: cid, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errBadPasskey
	}
	if err != nil {
		return nil, err
	}
	if row.Purpose != purpose || row.SessionID != session || !row.ExpiresAt.Time.After(s.now()) {
		return nil, errBadPasskey
	}
	var data webauthn.SessionData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func parseAssertion(a *panelv1.PasskeyAnswer) (*protocol.ParsedCredentialAssertionData, error) {
	if len(a.GetCredentialJson()) > maxCredentialJSON {
		return nil, errBadPasskey
	}
	p, err := protocol.ParseCredentialRequestResponseBytes([]byte(a.GetCredentialJson()))
	if err != nil {
		return nil, errBadPasskey
	}
	return p, nil
}

// usePasskey records a passkey's answer: the new counter and flags. A
// counter that went backwards means the key may have been copied, so the
// answer is refused, as Wings does for signed commands.
func (s *Service) usePasskey(ctx context.Context, q *store.Queries, row store.Passkey, c *webauthn.Credential) error {
	if c.Authenticator.CloneWarning {
		s.log().Warn("passkey counter went backwards: refusing it", "passkey", uuid.UUID(row.ID.Bytes), "user", uuid.UUID(row.UserID.Bytes))
		return errBadPasskey
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return q.UsePasskey(ctx, store.UsePasskeyParams{ID: row.ID, Credential: raw})
}

// BeginPasskeySignIn implements AuthService.
func (s *Service) BeginPasskeySignIn(ctx context.Context, _ *panelv1.BeginPasskeySignInRequest) (*panelv1.BeginPasskeySignInResponse, error) {
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "ceremony:ip:"+s.clientIP(ctx).String(), limitCeremonyPerIP); err != nil {
		return nil, err
	}
	// Discoverable: the passkey says whose it is, so no email is asked for
	// and nothing reveals whether an account exists.
	assertion, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	ch, err := s.saveCeremony(ctx, "signin", pgtype.UUID{}, data, assertion.Response)
	if err != nil {
		return nil, err
	}
	return &panelv1.BeginPasskeySignInResponse{Challenge: ch}, nil
}

// FinishPasskeySignIn implements AuthService. A passkey is two factors
// (the device and the fingerprint or PIN that unlocks it), so it signs in
// on its own and counts as a re-authentication.
func (s *Service) FinishPasskeySignIn(ctx context.Context, req *panelv1.FinishPasskeySignInRequest) (*panelv1.FinishPasskeySignInResponse, error) {
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "check:ip:"+s.clientIP(ctx).String(), limitCheckPerIP); err != nil {
		return nil, err
	}
	parsed, err := parseAssertion(req.GetAnswer())
	if err != nil {
		return nil, err
	}
	// Taken outside the transaction, so a failed answer still uses it up.
	data, err := s.takeCeremony(ctx, s.q(), req.GetAnswer().GetCeremonyId(), "signin", pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	var out *panelv1.FinishPasskeySignInResponse
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var row store.Passkey
		var user store.User
		find := func(rawID, handle []byte) (webauthn.User, error) {
			var err error
			if row, err = q.PasskeyByCredentialID(ctx, rawID); err != nil {
				return nil, err
			}
			if user, err = q.GetUser(ctx, row.UserID); err != nil {
				return nil, err
			}
			if !bytes.Equal(user.WebauthnHandle, handle) {
				return nil, errors.New("user handle doesn't match")
			}
			u, _, err := loadWAUser(ctx, q, user)
			return u, err
		}
		_, cred, err := wa.ValidatePasskeyLogin(find, *data, parsed)
		if err != nil {
			s.log().Info("passkey sign-in refused", "ip", s.clientIP(ctx), "err", err)
			return errBadPasskey
		}
		if err := s.usePasskey(ctx, q, row, cred); err != nil {
			return err
		}
		if err := s.startSession(ctx, q, user, true); err != nil {
			return err
		}
		out = &panelv1.FinishPasskeySignInResponse{User: userProto(user)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BeginPasskeyRegistration implements AuthService.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, _ *panelv1.BeginPasskeyRegistrationRequest) (*panelv1.BeginPasskeyRegistrationResponse, error) {
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	q := s.q()
	handle := make([]byte, 32)
	_, _ = rand.Read(handle)
	if sess.User.WebauthnHandle, err = q.SetWebAuthnHandle(ctx, store.SetWebAuthnHandleParams{ID: sess.UserID, Handle: handle}); err != nil {
		return nil, err
	}
	u, _, err := loadWAUser(ctx, q, sess.User)
	if err != nil {
		return nil, err
	}
	if len(u.creds) >= MaxPasskeys {
		return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("an account can have up to %d passkeys; remove one first", MaxPasskeys))
	}
	creation, data, err := wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			// Discoverable, so it can sign in without an email; and user
			// verification, so it's two factors.
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		// The same authenticator twice would just be confusing.
		webauthn.WithExclusions(webauthn.Credentials(u.creds).CredentialDescriptors()),
	)
	if err != nil {
		return nil, err
	}
	ch, err := s.saveCeremony(ctx, "register", sess.ID, data, creation.Response)
	if err != nil {
		return nil, err
	}
	return &panelv1.BeginPasskeyRegistrationResponse{Challenge: ch}, nil
}

// FinishPasskeyRegistration implements AuthService.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, req *panelv1.FinishPasskeyRegistrationRequest) (*panelv1.FinishPasskeyRegistrationResponse, error) {
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	name, err := passkeyName(req.GetName())
	if err != nil {
		return nil, err
	}
	if len(req.GetAnswer().GetCredentialJson()) > maxCredentialJSON {
		return nil, errBadPasskey
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes([]byte(req.GetAnswer().GetCredentialJson()))
	if err != nil {
		return nil, errBadPasskey
	}
	data, err := s.takeCeremony(ctx, s.q(), req.GetAnswer().GetCeremonyId(), "register", sess.ID)
	if err != nil {
		return nil, err
	}
	var row store.Passkey
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		u, _, err := loadWAUser(ctx, q, sess.User)
		if err != nil {
			return err
		}
		cred, err := wa.CreateCredential(u, *data, parsed)
		if err != nil {
			s.log().Info("passkey registration refused", "user", uuid.UUID(sess.UserID.Bytes), "err", err)
			return errBadPasskey
		}
		raw, err := json.Marshal(cred)
		if err != nil {
			return err
		}
		row, err = q.CreatePasskey(ctx, store.CreatePasskeyParams{UserID: sess.UserID, CredentialID: cred.ID, Credential: raw, Name: name})
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("that passkey is already registered"))
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, "A passkey was added to your Raptor account",
		fmt.Sprintf("A passkey named %q was added to your Raptor account. It can sign in without a code.\n\nIf this wasn't you, sign out every device and remove it from your account settings right away.\n", name))
	return &panelv1.FinishPasskeyRegistrationResponse{Passkey: passkeyProto(row)}, nil
}

// ListPasskeys implements AuthService.
func (s *Service) ListPasskeys(ctx context.Context, _ *panelv1.ListPasskeysRequest) (*panelv1.ListPasskeysResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q().ListPasskeys(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListPasskeysResponse{}
	for _, r := range rows {
		out.Passkeys = append(out.Passkeys, passkeyProto(r))
	}
	return out, nil
}

// RenamePasskey implements AuthService. Only a label, so no re-auth.
func (s *Service) RenamePasskey(ctx context.Context, req *panelv1.RenamePasskeyRequest) (*panelv1.RenamePasskeyResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	name, err := passkeyName(req.GetName())
	if err != nil {
		return nil, err
	}
	n, err := s.q().RenamePasskey(ctx, store.RenamePasskeyParams{ID: id, UserID: sess.UserID, Name: name})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errNoPasskey
	}
	return &panelv1.RenamePasskeyResponse{}, nil
}

// DeletePasskey implements AuthService. An email code always works, so
// removing the last passkey never locks anyone out.
func (s *Service) DeletePasskey(ctx context.Context, req *panelv1.DeletePasskeyRequest) (*panelv1.DeletePasskeyResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	row, err := s.q().DeletePasskey(ctx, store.DeletePasskeyParams{ID: id, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoPasskey
	}
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, "A passkey was removed from your Raptor account",
		fmt.Sprintf("The passkey named %q was removed from your Raptor account.\n\nIf this wasn't you, sign out every device from your account settings right away.\n", row.Name))
	return &panelv1.DeletePasskeyResponse{}, nil
}

func passkeyProto(r store.Passkey) *panelv1.Passkey {
	out := &panelv1.Passkey{Id: uuid.UUID(r.ID.Bytes).String(), Name: r.Name, CreatedAt: timestamppb.New(r.CreatedAt.Time)}
	if r.LastUsedAt.Valid {
		out.LastUsedAt = timestamppb.New(r.LastUsedAt.Time)
	}
	var c webauthn.Credential
	if json.Unmarshal(r.Credential, &c) == nil {
		out.Synced = c.Flags.BackupEligible
	}
	return out
}

func passkeyName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "Passkey", nil
	}
	if !utf8.ValidString(n) || utf8.RuneCountInString(n) > maxPasskeyName || strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a passkey's name is up to %d characters", maxPasskeyName))
	}
	return n, nil
}

func parseID(s string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, connect.NewError(connect.CodeInvalidArgument, errors.New("bad ID"))
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
