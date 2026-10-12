package staff

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// statsLock is the advisory lock a sample holds, so one Panel instance
// writes each hour's row.
const statsLock = 0x7261_7074_7374_6174 // "raptstat"

// RunStats records the platform's counts every hour for the overview's
// graphs, until ctx ends.
func (s *Service) RunStats(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if err := s.SampleStats(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Warn("recording platform stats failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SampleStats writes this hour's counts, once.
func (s *Service) SampleStats(ctx context.Context) error {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(statsLock)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(statsLock))
	}()
	q := store.New(s.DB)
	c, err := q.PlatformCounts(ctx)
	if err != nil {
		return err
	}
	return q.InsertPlatformStats(ctx, store.InsertPlatformStatsParams{
		At:    pgtype.Timestamptz{Time: s.Auth.Clock().Truncate(time.Hour), Valid: true},
		Users: c.Users, Orgs: c.Orgs, Nodes: c.Nodes, NodesConnected: c.NodesConnected,
		Servers: c.Servers, ServersRunning: c.ServersRunning, StorageBytes: c.StorageBytes,
	})
}
