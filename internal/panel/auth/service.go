package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/store"
)

var _ panelv1connect.AuthServiceHandler = (*Service)(nil)

// StartEmailSignIn emails a code and a link. The answer never says whether
// the address has an account.
func (s *Service) StartEmailSignIn(ctx context.Context, req *panelv1.StartEmailSignInRequest) (*panelv1.StartEmailSignInResponse, error) {
	email, err := normalizeEmail(req.GetEmail())
	if err != nil {
		return nil, err
	}
	ip := s.clientIP(ctx)
	if s.Turnstile != nil {
		if err := s.Turnstile.Verify(ctx, req.GetTurnstileToken(), ip.String()); err != nil {
			s.log().Info("turnstile refused", "ip", ip, "err", err)
			return nil, errChallenged
		}
	}
	if err := s.limit(ctx, "send:ip:"+ip.String(), limitSendPerIP); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "send:email:"+email, limitSendPerEmail); err != nil {
		return nil, err
	}
	if err := s.sendEmailCode(ctx, email, purposeSignIn); err != nil {
		return nil, err
	}
	return &panelv1.StartEmailSignInResponse{}, nil
}

// Email code purposes: a sign-in code can't confirm a sensitive change, and a
// re-authentication code can't sign in.
const (
	purposeSignIn = "signin"
	purposeReauth = "reauth"
)

// sendEmailCode emails a code (and, for signing in, a link).
func (s *Service) sendEmailCode(ctx context.Context, email, purpose string) error {
	if s.Mailer == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("email isn't set up on this Panel"))
	}
	code, link := newCode(), newToken()
	linkHash := hash(link)
	if err := s.q().CreateEmailCode(ctx, store.CreateEmailCodeParams{
		Email: email, CodeHash: hash(string(linkHash), code), LinkTokenHash: linkHash,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(CodeTTL), Valid: true}, Purpose: purpose,
	}); err != nil {
		return err
	}
	var subject, body string
	if purpose == purposeSignIn {
		// The token goes after #, so it never reaches a server's logs.
		url := strings.TrimSuffix(s.AppURL, "/") + "/signin/link#" + link
		subject = "Your Raptor sign-in code: " + code
		body = fmt.Sprintf("Your Raptor sign-in code is %s\n\nOr sign in with this link:\n%s\n\nBoth work once, for %d minutes. If you didn't ask for this, ignore it: nobody can sign in without the code.\n",
			code, url, int(CodeTTL.Minutes()))
	} else {
		subject = "Your Raptor confirmation code: " + code
		body = fmt.Sprintf("Your Raptor confirmation code is %s\n\nSomeone signed in to your account is changing how it's secured, and asked to confirm it's you. The code works once, for %d minutes. If this wasn't you, sign out every device from your account settings.\n",
			code, int(CodeTTL.Minutes()))
	}
	if err := s.Mailer.Send(ctx, email, subject, body); err != nil {
		s.log().Error("sending an email failed", "purpose", purpose, "err", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("couldn't send the email; try again in a minute"))
	}
	return nil
}

// checkEmailCode checks a code against the newest one sent to email for
// purpose and uses it up. A wrong code counts against its attempts; failure
// is the error the user sees.
func checkEmailCode(ctx context.Context, q *store.Queries, now time.Time, email, purpose, code string) (row store.EmailCode, failure, err error) {
	row, err = q.LatestEmailCode(ctx, store.LatestEmailCodeParams{Email: email, Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errBadCode, nil
	}
	if err != nil {
		return row, nil, err
	}
	if row.Attempts >= CodeAttempts || !row.ExpiresAt.Time.After(now) {
		return row, errBadCode, nil
	}
	given := hash(string(row.LinkTokenHash), strings.TrimSpace(code))
	if subtle.ConstantTimeCompare(given, row.CodeHash) != 1 {
		// The attempt counts even though the answer is "wrong".
		return row, errBadCode, q.CountEmailCodeAttempt(ctx, row.ID)
	}
	return row, nil, q.UseEmailCode(ctx, row.ID)
}

// FinishEmailSignIn checks a code or a link and starts a session, creating
// the account the first time (the code proves the address).
func (s *Service) FinishEmailSignIn(ctx context.Context, req *panelv1.FinishEmailSignInRequest) (*panelv1.FinishEmailSignInResponse, error) {
	if err := s.limit(ctx, "check:ip:"+s.clientIP(ctx).String(), limitCheckPerIP); err != nil {
		return nil, err
	}
	var out *panelv1.FinishEmailSignInResponse
	var failure error
	after := func() {}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var row store.EmailCode
		switch p := req.GetProof().(type) {
		case *panelv1.FinishEmailSignInRequest_Code:
			email, err := normalizeEmail(p.Code.GetEmail())
			if err != nil {
				return err
			}
			var f error
			row, f, err = checkEmailCode(ctx, q, s.now(), email, purposeSignIn, p.Code.GetCode())
			if err != nil {
				return err
			}
			if f != nil {
				failure = f
				// A wrong code for an account goes in its activity.
				ev := Event{Action: "signin.failed", Meta: map[string]any{"method": "email", "reason": "code"}}
				if u, err := q.GetUserByEmail(ctx, email); err == nil {
					ev.User = u.ID
				}
				return s.Audit(ctx, q, ev)
			}
		case *panelv1.FinishEmailSignInRequest_LinkToken:
			var err error
			row, err = q.EmailCodeByLink(ctx, hash(p.LinkToken))
			if errors.Is(err, pgx.ErrNoRows) {
				failure = errBadLink
				return nil
			}
			if err != nil {
				return err
			}
			if row.UsedAt.Valid || !row.ExpiresAt.Time.After(s.now()) {
				failure = errBadLink
				return nil
			}
			if err := q.UseEmailCode(ctx, row.ID); err != nil {
				return err
			}
		default:
			return connect.NewError(connect.CodeInvalidArgument, errors.New("a code or a link is needed"))
		}
		user, err := q.GetUserByEmail(ctx, row.Email)
		created := false
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if user, err = q.CreateUser(ctx, row.Email); err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		default:
			if err := q.VerifyUserEmail(ctx, user.ID); err != nil {
				return err
			}
		}
		// With TOTP on, the email is only the first factor.
		if user.TotpEnabledAt.Valid {
			out = &panelv1.FinishEmailSignInResponse{SecondFactorRequired: true}
			return s.startPending(ctx, q, user, "email")
		}
		// An email proves the inbox, which is enough for sensitive changes
		// only on accounts with nothing stronger: otherwise someone who
		// got into the inbox could sign in and remove the passkeys.
		strong, err := hasStrongMethod(ctx, q, user)
		if err != nil {
			return err
		}
		if after, err = s.startSession(ctx, q, user, !strong, "email"); err != nil {
			return err
		}
		out = &panelv1.FinishEmailSignInResponse{User: userProto(user), NewAccount: created}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	after()
	return out, nil
}

func userProto(u store.User) *panelv1.User {
	return &panelv1.User{Id: uuid.UUID(u.ID.Bytes).String(), Email: u.Email, Name: u.Name, TotpEnabled: u.TotpEnabledAt.Valid}
}

func sessionProto(sess store.Session, current pgtype.UUID) *panelv1.Session {
	out := &panelv1.Session{
		Id: uuid.UUID(sess.ID.Bytes).String(), CreatedAt: timestamppb.New(sess.CreatedAt.Time),
		LastSeenAt: timestamppb.New(sess.LastSeenAt.Time), UserAgent: sess.UserAgent, Current: sess.ID == current,
	}
	if sess.Ip != nil {
		out.Ip = sess.Ip.String()
	}
	return out
}

// GetSession returns who's signed in.
func (s *Service) GetSession(ctx context.Context, _ *panelv1.GetSessionRequest) (*panelv1.GetSessionResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	out := sessionProto(sess.Session, sess.ID)
	if until := s.reauthUntil(sess); !until.IsZero() {
		out.ReauthUntil = timestamppb.New(until)
	}
	res := &panelv1.GetSessionResponse{User: userProto(sess.User), Session: out}
	if sess.User.TotpEnabledAt.Valid {
		n, err := s.q().CountRecoveryCodes(ctx, sess.UserID)
		if err != nil {
			return nil, err
		}
		res.RecoveryCodesLeft = int32(n) //nolint:gosec // at most RecoveryCodeCount
	}
	return res, nil
}

// SignOut ends this session and clears the cookie.
func (s *Service) SignOut(ctx context.Context, _ *panelv1.SignOutRequest) (*panelv1.SignOutResponse, error) {
	if sess, err := s.Current(ctx); err == nil {
		if err := s.q().RevokeSession(ctx, store.RevokeSessionParams{ID: sess.ID, UserID: sess.UserID}); err != nil {
			return nil, err
		}
		_ = s.Audit(ctx, nil, Event{User: sess.UserID, Action: "session.signout", Target: uuid.UUID(sess.ID.Bytes).String()})
	}
	if c, ok := callOf(ctx); ok {
		setCookie(c.resp, "", 0)
	}
	return &panelv1.SignOutResponse{}, nil
}

// ListSessions lists the user's devices.
func (s *Service) ListSessions(ctx context.Context, _ *panelv1.ListSessionsRequest) (*panelv1.ListSessionsResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q().ListUserSessions(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListSessionsResponse{}
	for _, r := range rows {
		if s.now().Sub(r.LastSeenAt.Time) > SessionIdle {
			continue
		}
		out.Sessions = append(out.Sessions, sessionProto(r, sess.ID))
	}
	return out, nil
}

// RevokeSession signs a device out.
func (s *Service) RevokeSession(ctx context.Context, req *panelv1.RevokeSessionRequest) (*panelv1.RevokeSessionResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	q := s.q()
	switch t := req.GetTarget().(type) {
	case *panelv1.RevokeSessionRequest_AllOthers:
		err = q.RevokeOtherSessions(ctx, store.RevokeOtherSessionsParams{UserID: sess.UserID, ID: sess.ID})
	case *panelv1.RevokeSessionRequest_Id:
		id, perr := uuid.Parse(t.Id)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad session ID"))
		}
		// Bound to the user: someone else's session ID does nothing.
		err = q.RevokeSession(ctx, store.RevokeSessionParams{ID: pgtype.UUID{Bytes: id, Valid: true}, UserID: sess.UserID})
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("which session?"))
	}
	if err != nil {
		return nil, err
	}
	target := "all_others"
	if t, ok := req.GetTarget().(*panelv1.RevokeSessionRequest_Id); ok {
		target = t.Id
	}
	_ = s.Audit(ctx, nil, Event{User: sess.UserID, Action: "session.revoke", Target: target})
	return &panelv1.RevokeSessionResponse{}, nil
}
