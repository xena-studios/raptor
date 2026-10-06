package auth

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// AsUser runs fn in a transaction as the signed-in user: as the database
// role raptor_app with raptor.user_id set, so row-level security only shows
// their orgs (db/panel/migrations/00013_rls.sql) even if a check in the
// handler is missing.
func (s *Service) AsUser(ctx context.Context, fn func(sess *Session, q *store.Queries) error) error {
	sess, err := s.Current(ctx)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE raptor_app"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('raptor.user_id', $1, true)", uuid.UUID(sess.UserID.Bytes).String()); err != nil {
			return err
		}
		return fn(sess, store.New(tx))
	})
}
