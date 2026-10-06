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
)

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
