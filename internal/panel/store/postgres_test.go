package store

import (
	"context"
	"os"
	"testing"
)

// Requires a disposable database: PANEL_TEST_DATABASE_URL=postgres://...
func TestMigrateAndPing(t *testing.T) {
	url := os.Getenv("PANEL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PANEL_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ok, err := New(pool).Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok != 1 {
		t.Fatalf("ping = %d, want 1", ok)
	}
}
