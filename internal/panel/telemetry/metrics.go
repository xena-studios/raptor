package telemetry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Postgres reports the database's health every collection: connections in
// the Panel's pool, the streaming replica (how many, and how far behind),
// the WAL archive (failures, and how long since a segment made it), and how
// much WAL is on disk.
// The replica and archive views need the pg_monitor role
// (deploy/primary/initdb).
func Postgres(pool *pgxpool.Pool) error {
	m := otel.GetMeterProvider().Meter(scope) // the provider in place now
	conns, err := m.Int64ObservableGauge("raptor.postgres.pool.connections", metric.WithDescription("The Panel's database connections, by state"))
	if err != nil {
		return err
	}
	replicas, err := m.Int64ObservableGauge("raptor.postgres.replicas", metric.WithDescription("Streaming replicas connected"))
	if err != nil {
		return err
	}
	lag, err := m.Float64ObservableGauge("raptor.postgres.replica.lag", metric.WithUnit("s"), metric.WithDescription("The furthest replica's replay lag"))
	if err != nil {
		return err
	}
	failed, err := m.Int64ObservableGauge("raptor.postgres.archive.failures", metric.WithDescription("WAL segments that failed to archive, since the stats were reset"))
	if err != nil {
		return err
	}
	since, err := m.Float64ObservableGauge("raptor.postgres.archive.age", metric.WithUnit("s"), metric.WithDescription("Time since a WAL segment was last archived"))
	if err != nil {
		return err
	}
	wal, err := m.Int64ObservableGauge("raptor.postgres.wal.size", metric.WithUnit("By"), metric.WithDescription("WAL on the primary's disk: grows when the archive or a replica falls behind"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		st := pool.Stat()
		o.ObserveInt64(conns, int64(st.AcquiredConns()), metric.WithAttributes(attribute.String("state", "used")))
		o.ObserveInt64(conns, int64(st.IdleConns()), metric.WithAttributes(attribute.String("state", "idle")))
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var n int64
		var lagSec float64
		if pool.QueryRow(ctx, `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM max(replay_lag)), 0)::float8 FROM pg_stat_replication`).Scan(&n, &lagSec) == nil {
			o.ObserveInt64(replicas, n)
			o.ObserveFloat64(lag, lagSec)
		}
		var fails int64
		var age *float64
		if pool.QueryRow(ctx, `SELECT failed_count, EXTRACT(EPOCH FROM now() - last_archived_time)::float8 FROM pg_stat_archiver`).Scan(&fails, &age) == nil {
			o.ObserveInt64(failed, fails)
			if age != nil {
				o.ObserveFloat64(since, *age)
			}
		}
		var walBytes int64
		if pool.QueryRow(ctx, `SELECT COALESCE(sum(size), 0)::bigint FROM pg_ls_waldir()`).Scan(&walBytes) == nil {
			o.ObserveInt64(wal, walBytes)
		}
		return nil
	}, conns, replicas, lag, failed, since, wal)
	return err
}
