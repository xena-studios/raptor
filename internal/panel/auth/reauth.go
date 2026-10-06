package auth

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// ReauthTTL is how long a re-authentication allows sensitive account
// changes (docs/PANEL.md#auth).
const ReauthTTL = 5 * time.Minute

// reauthUntil is when the session's re-authentication runs out (zero if it
// has).
func (s *Service) reauthUntil(sess *Session) time.Time {
	if !sess.ReauthAt.Valid {
		return time.Time{}
	}
	until := sess.ReauthAt.Time.Add(ReauthTTL)
	if !until.After(s.now()) {
		return time.Time{}
	}
	return until
}

// requireReauth refuses a sensitive change unless the session
// re-authenticated in the last 5 minutes. The web app sees
// FAILED_PRECONDITION, runs BeginReauth, and retries.
func (s *Service) requireReauth(sess *Session) error {
	if s.reauthUntil(sess).IsZero() {
		return errReauth
	}
	return nil
}

// BeginReauth implements AuthService: a passkey if the account has one,
// otherwise an emailed code (TOTP joins when it exists).
func (s *Service) BeginReauth(ctx context.Context, _ *panelv1.BeginReauthRequest) (*panelv1.BeginReauthResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "reauth:user:"+uuid.UUID(sess.UserID.Bytes).String(), limitReauthPerUser); err != nil {
		return nil, err
	}
	q := s.q()
	u, _, err := loadWAUser(ctx, q, sess.User)
	if err != nil {
		return nil, err
	}
	totpOn := sess.User.TotpEnabledAt.Valid
	if len(u.creds) == 0 && totpOn {
		return &panelv1.BeginReauthResponse{TotpAllowed: true}, nil
	}
	if len(u.creds) == 0 {
		if err := s.limit(ctx, "send:email:"+sess.User.Email, limitSendPerEmail); err != nil {
			return nil, err
		}
		if err := s.sendEmailCode(ctx, sess.User.Email, purposeReauth); err != nil {
			return nil, err
		}
		return &panelv1.BeginReauthResponse{Method: &panelv1.BeginReauthResponse_EmailSent{EmailSent: true}}, nil
	}
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	assertion, data, err := wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	ch, err := s.saveCeremony(ctx, "reauth", sess.ID, data, assertion.Response)
	if err != nil {
		return nil, err
	}
	return &panelv1.BeginReauthResponse{Method: &panelv1.BeginReauthResponse_Passkey{Passkey: ch}, TotpAllowed: totpOn}, nil
}

// FinishReauth implements AuthService.
func (s *Service) FinishReauth(ctx context.Context, req *panelv1.FinishReauthRequest) (*panelv1.FinishReauthResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "check:ip:"+s.clientIP(ctx).String(), limitCheckPerIP); err != nil {
		return nil, err
	}
	var failure error
	var method string
	switch p := req.GetProof().(type) {
	case *panelv1.FinishReauthRequest_Passkey:
		method = "passkey"
		failure, err = s.reauthPasskey(ctx, sess, p.Passkey)
	case *panelv1.FinishReauthRequest_EmailCode:
		method = "email"
		failure, err = s.reauthEmail(ctx, sess, p.EmailCode)
	case *panelv1.FinishReauthRequest_TotpCode:
		method = "totp"
		failure, err = s.reauthTOTP(ctx, sess, p.TotpCode)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a passkey or a code is needed"))
	}
	if err != nil {
		return nil, err
	}
	ev := Event{User: sess.UserID, Action: "reauth", Target: uuid.UUID(sess.ID.Bytes).String(), Meta: map[string]any{"method": method}}
	if failure != nil {
		ev.Action = "reauth.failed"
	}
	_ = s.Audit(ctx, nil, ev)
	if failure != nil {
		return nil, failure
	}
	return &panelv1.FinishReauthResponse{ReauthUntil: timestamppb.New(s.now().Add(ReauthTTL))}, nil
}

func (s *Service) reauthPasskey(ctx context.Context, sess *Session, a *panelv1.PasskeyAnswer) (failure, err error) {
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	parsed, err := parseAssertion(a)
	if err != nil {
		return err, nil
	}
	data, err := s.takeCeremony(ctx, s.q(), a.GetCeremonyId(), "reauth", sess.ID)
	if err != nil {
		return err, nil
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Lock the passkey before reading its counter.
		row, err := q.PasskeyByCredentialID(ctx, parsed.RawID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.UserID != sess.UserID) {
			failure = errBadPasskey
			return nil
		}
		if err != nil {
			return err
		}
		u, _, err := loadWAUser(ctx, q, sess.User)
		if err != nil {
			return err
		}
		cred, err := wa.ValidateLogin(u, *data, parsed)
		if err != nil {
			s.log().Info("passkey re-authentication refused", "user", uuid.UUID(sess.UserID.Bytes), "err", err)
			failure = errBadPasskey
			return nil
		}
		if err := s.usePasskey(ctx, q, row, cred); err != nil {
			if errors.Is(err, errBadPasskey) {
				failure = err
				return nil
			}
			return err
		}
		return q.SetSessionReauth(ctx, store.SetSessionReauthParams{ID: sess.ID, ReauthAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	})
	return failure, err
}

func (s *Service) reauthEmail(ctx context.Context, sess *Session, code string) (failure, err error) {
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Only for accounts with nothing stronger than the inbox.
		strong, err := hasStrongMethod(ctx, q, sess.User)
		if err != nil {
			return err
		}
		if strong {
			failure = connect.NewError(connect.CodeFailedPrecondition, errors.New("this account has a passkey or an authenticator app: confirm with it instead"))
			return nil
		}
		_, f, err := checkEmailCode(ctx, q, s.now(), sess.User.Email, purposeReauth, code)
		if err != nil || f != nil {
			failure = f
			return err
		}
		return q.SetSessionReauth(ctx, store.SetSessionReauthParams{ID: sess.ID, ReauthAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	})
	return failure, err
}

func (s *Service) reauthTOTP(ctx context.Context, sess *Session, code string) (failure, err error) {
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		ok, err := s.checkTOTP(ctx, q, sess.UserID, code)
		if err != nil {
			return err
		}
		if !ok {
			failure = errBadTOTP
			return nil
		}
		return q.SetSessionReauth(ctx, store.SetSessionReauthParams{ID: sess.ID, ReauthAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	})
	return failure, err
}

// notify emails the user about a change to how their account is secured.
// Best effort: the change already happened.
func (s *Service) notify(ctx context.Context, user store.User, subject, body string) {
	if s.Mailer == nil {
		return
	}
	if err := s.Mailer.Send(context.WithoutCancel(ctx), user.Email, subject, body); err != nil {
		s.log().Error("sending a security notice failed", "user", uuid.UUID(user.ID.Bytes), "err", err)
	}
}
