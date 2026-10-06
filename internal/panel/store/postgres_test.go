package store_test

import (
	"context"
	"testing"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Migrations apply to a fresh database (paneltest.NewDB) and again as a
// no-op.
func TestMigrateAndPing(t *testing.T) {
	ctx := context.Background()
	pool := paneltest.NewDB(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ok, err := store.New(pool).Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok != 1 {
		t.Fatalf("ping = %d, want 1", ok)
	}
}
