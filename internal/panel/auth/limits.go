package auth

import (
	"context"
	"time"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Limit is how many events a key may have in a window.
type Limit struct {
	Max    int64
	Window time.Duration
}

// Limits on signing in (docs/PANEL.md#auth): per address and per IP, so the
// Panel can't be used to flood an inbox or guess codes.
var (
	limitSendPerEmail = Limit{5, time.Hour}
	limitSendPerIP    = Limit{20, time.Hour}
	limitCheckPerIP   = Limit{50, time.Hour}
	// Starting a passkey ceremony stores a challenge.
	limitCeremonyPerIP = Limit{100, time.Hour}
	limitReauthPerUser = Limit{20, time.Hour}
)

// limit is allow as an error: RESOURCE_EXHAUSTED when over.
func (s *Service) limit(ctx context.Context, key string, l Limit) error {
	ok, err := s.allow(ctx, key, l)
	if err != nil {
		return err
	}
	if !ok {
		return errRateLimit
	}
	return nil
}

// allow counts an event for key and reports whether it's within l. Counts
// are in Postgres, so every Panel instance shares them.
func (s *Service) allow(ctx context.Context, key string, l Limit) (bool, error) {
	q := s.q()
	n, err := q.CountRateEvents(ctx, store.CountRateEventsParams{Key: key, WindowSecs: l.Window.Seconds()})
	if err != nil {
		return false, err
	}
	if n >= l.Max {
		return false, nil
	}
	return true, q.AddRateEvent(ctx, key)
}

// Prune deletes what's no longer needed: old rate events, expired codes and
// ceremonies, and sessions long past their end.
func (s *Service) Prune(ctx context.Context) error {
	q := s.q()
	for _, f := range []func(context.Context) error{q.PruneRateEvents, q.PruneEmailCodes, q.PruneCeremonies, q.PruneSessions, q.PrunePending, q.PruneTOTPSetups} {
		if err := f(ctx); err != nil {
			return err
		}
	}
	return nil
}

// RunJanitor prunes every interval until ctx ends. Every instance runs it;
// the deletes are idempotent.
func (s *Service) RunJanitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Prune(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("pruning auth tables failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
