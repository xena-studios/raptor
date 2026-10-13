package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/xena-studios/raptor/db/panel/migrations"
)

// minConns is the fewest connections a Panel's pool gets. pgxpool's
// default is max(4, CPUs), and each leader loop (backup keys, rollouts,
// stats, storage) holds a connection for its advisory lock while it asks
// the pool for another to work: on a 2-CPU server three of them and the
// LISTEN connection took all four, and every one waited forever (v0.3.0).
const minConns = 16

// poolConfig is the pool's settings: the URL's, with at least minConns
// connections unless the URL sets pool_max_conns itself.
func poolConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(databaseURL, "pool_max_conns") && cfg.MaxConns < minConns {
		cfg.MaxConns = minConns
	}
	return cfg, nil
}

// Open connects to Postgres.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
