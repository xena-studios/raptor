package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

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
	for _, c := range []struct {
		key string
		l   Limit
	}{{"send:ip:" + ip.String(), limitSendPerIP}, {"send:email:" + email, limitSendPerEmail}} {
		ok, err := s.allow(ctx, c.key, c.l)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errRateLimit
		}
	}
	if s.Mailer == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("signing in by email isn't set up on this Panel"))
	}
	code, link := newCode(), newToken()
	linkHash := hash(link)
	if err := s.q().CreateEmailCode(ctx, store.CreateEmailCodeParams{
		Email: email, CodeHash: hash(string(linkHash), code), LinkTokenHash: linkHash,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(CodeTTL), Valid: true},
	}); err != nil {
		return nil, err
	}
	// The token goes after #, so it never reaches a server's logs.
	url := strings.TrimSuffix(s.AppURL, "/") + "/signin/link#" + link
	body := fmt.Sprintf("Your Raptor sign-in code is %s\n\nOr sign in with this link:\n%s\n\nBoth work once, for %d minutes. If you didn't ask for this, ignore it: nobody can sign in without the code.\n",
		code, url, int(CodeTTL.Minutes()))
	if err := s.Mailer.Send(ctx, email, "Your Raptor sign-in code: "+code, body); err != nil {
		s.log().Error("sending a sign-in email failed", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("couldn't send the email; try again in a minute"))
	}
	return &panelv1.StartEmailSignInResponse{}, nil
}

// FinishEmailSignIn checks a code or a link and starts a session, creating
// the account the first time (the code proves the address).
func (s *Service) FinishEmailSignIn(ctx context.Context, req *panelv1.FinishEmailSignInRequest) (*panelv1.FinishEmailSignInResponse, error) {
	ip := s.clientIP(ctx)
	ok, err := s.allow(ctx, "check:ip:"+ip.String(), limitCheckPerIP)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errRateLimit
	}
	var out *panelv1.FinishEmailSignInResponse
	var failure error
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		var row store.EmailCode
		switch p := req.GetProof().(type) {
		case *panelv1.FinishEmailSignInRequest_Code:
			email, err := normalizeEmail(p.Code.GetEmail())
			if err != nil {
				return err
			}
			row, err = q.LatestEmailCode(ctx, email)
			if errors.Is(err, pgx.ErrNoRows) {
				failure = errBadCode
				return nil
			}
			if err != nil {
				return err
			}
			if row.Attempts >= CodeAttempts || !row.ExpiresAt.Time.After(s.now()) {
				failure = errBadCode
				return nil
			}
			given := hash(string(row.LinkTokenHash), strings.TrimSpace(p.Code.GetCode()))
			if subtle.ConstantTimeCompare(given, row.CodeHash) != 1 {
				failure = errBadCode
				// The attempt counts even though the answer is "wrong".
				return q.CountEmailCodeAttempt(ctx, row.ID)
			}
		case *panelv1.FinishEmailSignInRequest_LinkToken:
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
		default:
			return connect.NewError(connect.CodeInvalidArgument, errors.New("a code or a link is needed"))
		}
		if err := q.UseEmailCode(ctx, row.ID); err != nil {
			return err
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
		if err := s.startSession(ctx, q, user); err != nil {
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
	return out, nil
}

func userProto(u store.User) *panelv1.User {
	return &panelv1.User{Id: uuid.UUID(u.ID.Bytes).String(), Email: u.Email, Name: u.Name}
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
	return &panelv1.GetSessionResponse{User: userProto(sess.User), Session: sessionProto(sess.Session, sess.ID)}, nil
}

// SignOut ends this session and clears the cookie.
func (s *Service) SignOut(ctx context.Context, _ *panelv1.SignOutRequest) (*panelv1.SignOutResponse, error) {
	if sess, err := s.Current(ctx); err == nil {
		if err := s.q().RevokeSession(ctx, store.RevokeSessionParams{ID: sess.ID, UserID: sess.UserID}); err != nil {
			return nil, err
		}
	}
	if ci, ok := connect.CallInfoForHandlerContext(ctx); ok {
		setCookie(ci.ResponseHeader(), "", 0)
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
	return &panelv1.RevokeSessionResponse{}, nil
}
