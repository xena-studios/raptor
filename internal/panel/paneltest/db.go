// Package paneltest has helpers for Panel tests.
package paneltest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// NewDB is a migrated database of the test's own, dropped when the test
// ends. Tests in different packages run at the same time against the same
// server (PANEL_TEST_DATABASE_URL), so anything that counts rows across a
// table (rate limits, every node) needs its own.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("PANEL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PANEL_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
	})
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
