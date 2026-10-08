package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Themes are the looks an account can pick.
var themes = []string{"system", "light", "dark"}

// MaxNameLen is the longest name an account can have, in characters.
const MaxNameLen = 64

// purposeEmailChange is the purpose of a code confirming a new address for
// one account (the account's ID follows it), so a code sent for one account
// can't move another.
func purposeEmailChange(user [16]byte) string {
	return "email_change:" + uuid.UUID(user).String()
}

// UpdateProfile implements AuthService.
func (s *Service) UpdateProfile(ctx context.Context, req *panelv1.UpdateProfileRequest) (*panelv1.UpdateProfileResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	user := sess.User
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if req.Name != nil {
			name := strings.Join(strings.Fields(req.GetName()), " ")
			if utf8.RuneCountInString(name) > MaxNameLen {
				return invalid(fmt.Errorf("a name can be up to %d characters", MaxNameLen))
			}
			if user, err = q.SetUserName(ctx, store.SetUserNameParams{ID: sess.UserID, Name: name}); err != nil {
				return err
			}
		}
		if req.Theme != nil {
			if !slices.Contains(themes, req.GetTheme()) {
				return invalid(errors.New("the theme is system, light, or dark"))
			}
			if user, err = q.SetUserTheme(ctx, store.SetUserThemeParams{ID: sess.UserID, Theme: req.GetTheme()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.UpdateProfileResponse{User: userProto(user)}, nil
}

// StartEmailChange implements AuthService.
func (s *Service) StartEmailChange(ctx context.Context, req *panelv1.StartEmailChangeRequest) (*panelv1.StartEmailChangeResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	email, err := normalizeEmail(req.GetNewEmail())
	if err != nil {
		return nil, err
	}
	if email == sess.User.Email {
		return nil, invalid(errors.New("that's already your address"))
	}
	// Saying the address is taken is fine here: only a signed-in account that
	// just confirmed it's itself can ask, and it's rate limited.
	if _, err := s.q().GetUserByEmail(ctx, email); err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("that address is already on another account"))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := s.limit(ctx, "send:user:"+uuid.UUID(sess.UserID.Bytes).String(), limitSendPerEmail); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "send:email:"+email, limitSendPerEmail); err != nil {
		return nil, err
	}
	if err := s.sendEmailCode(ctx, email, purposeEmailChange(sess.UserID.Bytes)); err != nil {
		return nil, err
	}
	return &panelv1.StartEmailChangeResponse{}, nil
}

// FinishEmailChange implements AuthService. The code proves the new
// address; the re-authentication StartEmailChange needed proved the account.
func (s *Service) FinishEmailChange(ctx context.Context, req *panelv1.FinishEmailChangeRequest) (*panelv1.FinishEmailChangeResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	email, err := normalizeEmail(req.GetNewEmail())
	if err != nil {
		return nil, err
	}
	if over, err := s.over(ctx, "fail:email:"+email, limitFailedCodesPerEmail); err != nil {
		return nil, err
	} else if over {
		return nil, errRateLimit
	}
	var user store.User
	var failure error
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		_, f, err := checkEmailCode(ctx, q, s.now(), email, purposeEmailChange(sess.UserID.Bytes), req.GetCode())
		if err != nil {
			return err
		}
		if f != nil {
			failure = f
			return nil //nolint:nilerr // committed, so the wrong attempt counts
		}
		user, err = q.SetUserEmail(ctx, store.SetUserEmailParams{ID: sess.UserID, Email: email})
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("that address is already on another account"))
		}
		if err != nil {
			return err
		}
		return s.Audit(ctx, q, Event{User: sess.UserID, Action: "email.change", Meta: map[string]any{"from": sess.User.Email, "to": email}})
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		// Counted outside the transaction, which a wrong code doesn't roll back.
		_ = s.q().AddRateEvent(ctx, "fail:email:"+email)
		return nil, failure
	}
	// The old address hears about it, in case it wasn't its owner.
	s.notify(ctx, sess.User, "Your Raptor account's email changed",
		fmt.Sprintf("Your Raptor account now signs in with %s instead of this address.\n\nIf this wasn't you, reply to this email right away.\n", email))
	return &panelv1.FinishEmailChangeResponse{User: userProto(user)}, nil
}

// DeleteAccount implements AuthService.
func (s *Service) DeleteAccount(ctx context.Context, _ *panelv1.DeleteAccountRequest) (*panelv1.DeleteAccountResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		orgs, err := q.UserOrgs(ctx, sess.UserID)
		if err != nil {
			return err
		}
		for _, o := range orgs {
			if o.Role == "owner" {
				owners, err := q.LockOrgOwners(ctx, o.ID)
				if err != nil {
					return err
				}
				if len(owners) == 1 {
					if err := s.leaveLastOwned(ctx, q, o.ID, o.Name); err != nil {
						return err
					}
				}
			}
			// The org keeps a line saying who left; the account's email goes
			// in it, since the account won't be there to look up.
			if err := s.Audit(ctx, q, Event{Org: o.ID, User: sess.UserID, Action: "account.delete", Meta: map[string]any{"email": sess.User.Email}}); err != nil {
				return err
			}
		}
		if err := q.DeleteUserActivity(ctx, sess.UserID); err != nil {
			return err
		}
		// Sessions, passkeys, TOTP, SFTP passwords, linked accounts, memberships,
		// and server grants go with it (ON DELETE CASCADE).
		return q.DeleteUser(ctx, sess.UserID)
	})
	if err != nil {
		return nil, err
	}
	if c, ok := callOf(ctx); ok {
		setCookie(c.resp, "", 0)
	}
	s.notify(ctx, sess.User, "Your Raptor account was deleted",
		"Your Raptor account and everything that signed in to it were deleted. Signing in with this address again starts a new account.\n")
	return &panelv1.DeleteAccountResponse{}, nil
}

// leaveLastOwned checks that the account can leave an org it's the only
// owner of: nobody else is in it and it runs no nodes. The org is then left
// with no members, which nobody can see or join; its pending invitations
// are revoked so nobody can. (Orgs that had nodes are kept: node rows never
// go, so their hostnames are never reused.)
func (s *Service) leaveLastOwned(ctx context.Context, q *store.Queries, id pgtype.UUID, name string) error {
	members, err := q.CountOrgMembers(ctx, id)
	if err != nil {
		return err
	}
	if members > 1 {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("you're the only owner of %q: make another member an owner first", name))
	}
	nodes, err := q.CountOrgNodes(ctx, id)
	if err != nil {
		return err
	}
	if nodes > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%q still has nodes: remove them first", name))
	}
	return q.RevokeOrgInvitations(ctx, id)
}
