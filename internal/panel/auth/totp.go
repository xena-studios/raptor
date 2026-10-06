package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// TOTP settings: what every authenticator app does by default.
const (
	totpPeriod        = 30
	TOTPSetupTTL      = 15 * time.Minute
	RecoveryCodeCount = 10
	// PendingSigninTTL is how long a sign-in waits for its second factor.
	PendingSigninTTL = 10 * time.Minute
	PendingAttempts  = 5
)

var limitTOTPPerUser = Limit{20, time.Hour}

func (s *Service) aead() (cipher.AEAD, error) {
	if len(s.DataKey) != 32 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("two-factor authentication isn't set up on this Panel"))
	}
	block, err := aes.NewCipher(s.DataKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts a TOTP secret for one user: the user's ID is bound in, so a
// secret copied to another row doesn't decrypt. The first byte is the key
// version, for rotating PANEL_DATA_KEY later.
func (s *Service) seal(user pgtype.UUID, secret string) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{1}, nonce...)
	return a.Seal(out, nonce, []byte(secret), totpAAD(user)), nil
}

func (s *Service) open(user pgtype.UUID, sealed []byte) (string, error) {
	a, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(sealed) < 1+a.NonceSize() || sealed[0] != 1 {
		return "", errors.New("unknown TOTP secret format")
	}
	n := 1 + a.NonceSize()
	pt, err := a.Open(nil, sealed[1:n], sealed[n:], totpAAD(user))
	if err != nil {
		return "", fmt.Errorf("decrypting a TOTP secret: %w", err)
	}
	return string(pt), nil
}

func totpAAD(user pgtype.UUID) []byte { return append([]byte("raptor totp v1\x00"), user.Bytes[:]...) }

// matchTOTP checks a code against the time steps around now (one either
// way, for clock drift) and returns the step it matched. Steps at or before
// last are refused, so a code can't be used twice.
func matchTOTP(secret, code string, now time.Time, last int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / totpPeriod
	for _, st := range []int64{step - 1, step, step + 1} {
		if st <= last {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix(st*totpPeriod, 0), totp.ValidateOpts{
			Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return st, true
		}
	}
	return 0, false
}

// checkTOTP checks a code for a user with TOTP on, using up its time step.
// The user's row is locked, so two requests can't both use one code.
func (s *Service) checkTOTP(ctx context.Context, q *store.Queries, user pgtype.UUID, code string) (bool, error) {
	if err := s.limit(ctx, "totp:user:"+uuid.UUID(user.Bytes).String(), limitTOTPPerUser); err != nil {
		return false, err
	}
	row, err := q.LockUserTOTP(ctx, user)
	if err != nil {
		return false, err
	}
	if row.TotpSecret == nil {
		return false, nil
	}
	secret, err := s.open(user, row.TotpSecret)
	if err != nil {
		return false, err
	}
	step, ok := matchTOTP(secret, code, s.now(), row.TotpLastStep)
	if !ok {
		return false, nil
	}
	return true, q.SetTOTPStep(ctx, store.SetTOTPStepParams{ID: user, TotpLastStep: step})
}

// Recovery codes are 80 random bits, so a plain hash is enough to store
// them: there's nothing to guess offline.
var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func normalizeRecoveryCode(c string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(c)))
}

func recoveryHash(user pgtype.UUID, code string) []byte {
	return hash("recovery", string(user.Bytes[:]), normalizeRecoveryCode(code))
}

// newRecoveryCodes replaces a user's recovery codes and returns them.
func newRecoveryCodes(ctx context.Context, q *store.Queries, user pgtype.UUID) ([]string, error) {
	if err := q.DeleteRecoveryCodes(ctx, user); err != nil {
		return nil, err
	}
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		c := strings.ToLower(recoveryEncoding.EncodeToString(b))
		codes[i] = c[0:4] + "-" + c[4:8] + "-" + c[8:12] + "-" + c[12:16]
		if err := q.AddRecoveryCode(ctx, store.AddRecoveryCodeParams{UserID: user, CodeHash: recoveryHash(user, c)}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// BeginTOTPSetup implements AuthService.
func (s *Service) BeginTOTPSetup(ctx context.Context, _ *panelv1.BeginTOTPSetupRequest) (*panelv1.BeginTOTPSetupResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	if sess.User.TotpEnabledAt.Valid {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("two-factor authentication is already on"))
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Raptor", AccountName: sess.User.Email, Period: totpPeriod, SecretSize: 20})
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(sess.UserID, key.Secret())
	if err != nil {
		return nil, err
	}
	if err := s.q().SaveTOTPSetup(ctx, store.SaveTOTPSetupParams{
		UserID: sess.UserID, Secret: sealed, ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(TOTPSetupTTL), Valid: true},
	}); err != nil {
		return nil, err
	}
	return &panelv1.BeginTOTPSetupResponse{Secret: key.Secret(), Url: key.URL()}, nil
}

// FinishTOTPSetup implements AuthService.
func (s *Service) FinishTOTPSetup(ctx context.Context, req *panelv1.FinishTOTPSetupRequest) (*panelv1.FinishTOTPSetupResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "totp:user:"+uuid.UUID(sess.UserID.Bytes).String(), limitTOTPPerUser); err != nil {
		return nil, err
	}
	var codes []string
	var failure error
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		setup, err := q.TakeTOTPSetup(ctx, sess.UserID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !setup.ExpiresAt.Time.After(s.now())) {
			failure = connect.NewError(connect.CodeFailedPrecondition, errors.New("start setting up again"))
			return nil
		}
		if err != nil {
			return err
		}
		secret, err := s.open(sess.UserID, setup.Secret)
		if err != nil {
			return err
		}
		step, ok := matchTOTP(secret, req.GetCode(), s.now(), 0)
		if !ok {
			failure = errBadTOTP
			return nil
		}
		if err := q.EnableTOTP(ctx, store.EnableTOTPParams{ID: sess.UserID, TotpSecret: setup.Secret, TotpLastStep: step}); err != nil {
			return err
		}
		if err := q.DeleteTOTPSetup(ctx, sess.UserID); err != nil {
			return err
		}
		codes, err = newRecoveryCodes(ctx, q, sess.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	s.notify(ctx, sess.User, "Two-factor authentication is on for your Raptor account",
		"Signing in to your Raptor account by email now also needs a code from your authenticator app, or one of your recovery codes.\n\nIf this wasn't you, sign out every device from your account settings right away.\n")
	return &panelv1.FinishTOTPSetupResponse{RecoveryCodes: codes}, nil
}

// DisableTOTP implements AuthService.
func (s *Service) DisableTOTP(ctx context.Context, _ *panelv1.DisableTOTPRequest) (*panelv1.DisableTOTPResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	if !sess.User.TotpEnabledAt.Valid {
		return &panelv1.DisableTOTPResponse{}, nil
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.DisableTOTP(ctx, sess.UserID); err != nil {
			return err
		}
		return q.DeleteRecoveryCodes(ctx, sess.UserID)
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, "Two-factor authentication is off for your Raptor account",
		"Two-factor authentication was turned off, and your recovery codes no longer work.\n\nIf this wasn't you, sign out every device from your account settings and turn it back on right away.\n")
	return &panelv1.DisableTOTPResponse{}, nil
}

// RegenerateRecoveryCodes implements AuthService.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, _ *panelv1.RegenerateRecoveryCodesRequest) (*panelv1.RegenerateRecoveryCodesResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	if !sess.User.TotpEnabledAt.Valid {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("recovery codes come with two-factor authentication; turn it on first"))
	}
	var codes []string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		codes, err = newRecoveryCodes(ctx, store.New(tx), sess.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, "New recovery codes for your Raptor account",
		"New recovery codes were made for your Raptor account, and the old ones no longer work.\n\nIf this wasn't you, sign out every device from your account settings right away.\n")
	return &panelv1.RegenerateRecoveryCodesResponse{RecoveryCodes: codes}, nil
}

// pendingCookie holds a sign-in waiting for its second factor.
const pendingCookie = "__Host-raptor_signin"

// startPending starts a sign-in that needs a second factor.
func (s *Service) startPending(ctx context.Context, q *store.Queries, user store.User) error {
	token := newToken()
	if err := q.CreatePendingSignin(ctx, store.CreatePendingSigninParams{
		UserID: user.ID, TokenHash: hash(token), ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(PendingSigninTTL), Valid: true},
	}); err != nil {
		return err
	}
	if ci, ok := connect.CallInfoForHandlerContext(ctx); ok {
		setNamedCookie(ci.ResponseHeader(), pendingCookie, token, PendingSigninTTL)
	}
	return nil
}

// FinishSecondFactor implements AuthService.
func (s *Service) FinishSecondFactor(ctx context.Context, req *panelv1.FinishSecondFactorRequest) (*panelv1.FinishSecondFactorResponse, error) {
	ci, ok := connect.CallInfoForHandlerContext(ctx)
	if !ok {
		return nil, errNoPending
	}
	token := namedCookie(ci.RequestHeader(), pendingCookie)
	if token == "" {
		return nil, errNoPending
	}
	if err := s.limit(ctx, "check:ip:"+s.clientIP(ctx).String(), limitCheckPerIP); err != nil {
		return nil, err
	}
	var out *panelv1.FinishSecondFactorResponse
	var failure error
	var user store.User
	recovered := false
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		p, err := q.PendingSigninByToken(ctx, hash(token))
		if errors.Is(err, pgx.ErrNoRows) {
			failure = errNoPending
			return nil
		}
		if err != nil {
			return err
		}
		if p.Attempts >= PendingAttempts || !p.ExpiresAt.Time.After(s.now()) {
			failure = errNoPending
			return nil
		}
		var good bool
		switch c := req.GetProof().(type) {
		case *panelv1.FinishSecondFactorRequest_TotpCode:
			good, err = s.checkTOTP(ctx, q, p.UserID, c.TotpCode)
		case *panelv1.FinishSecondFactorRequest_RecoveryCode:
			var n int64
			n, err = q.UseRecoveryCode(ctx, store.UseRecoveryCodeParams{UserID: p.UserID, CodeHash: recoveryHash(p.UserID, c.RecoveryCode)})
			good, recovered = n == 1, n == 1
		default:
			return connect.NewError(connect.CodeInvalidArgument, errors.New("a code is needed"))
		}
		if err != nil {
			return err
		}
		if !good {
			failure = errBadTOTP
			return q.CountPendingSigninAttempt(ctx, p.ID)
		}
		if err := q.DeletePendingSignin(ctx, p.ID); err != nil {
			return err
		}
		if user, err = q.GetUser(ctx, p.UserID); err != nil {
			return err
		}
		// Both factors: it counts as a re-authentication, which someone who
		// lost their phone needs to set TOTP up again.
		if err := s.startSession(ctx, q, user, true); err != nil {
			return err
		}
		left, err := q.CountRecoveryCodes(ctx, user.ID)
		if err != nil {
			return err
		}
		out = &panelv1.FinishSecondFactorResponse{User: userProto(user)}
		if recovered {
			out.RecoveryCodesLeft = int32(left) //nolint:gosec // at most RecoveryCodeCount
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	setNamedCookie(ci.ResponseHeader(), pendingCookie, "", 0)
	if recovered {
		s.notify(ctx, user, "A recovery code was used on your Raptor account",
			fmt.Sprintf("Someone signed in to your Raptor account with a recovery code. %d are left.\n\nIf this wasn't you, sign out every device from your account settings and make new recovery codes right away.\n", out.GetRecoveryCodesLeft()))
	}
	return out, nil
}
