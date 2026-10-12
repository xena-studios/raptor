package catalog_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	catalog "github.com/xena-studios/raptor/eggs"
	seed "github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Seeding puts every catalog egg in the official library once; eggs only
// the seed changed follow the catalog, and eggs staff changed don't.
func TestSeed(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	all, err := catalog.All()
	if err != nil {
		t.Fatal(err)
	}
	added, updated, err := seed.Seed(ctx, db, log)
	if err != nil || added != len(all) || updated != 0 {
		t.Fatalf("first seed: %d added, %d updated, %v", added, updated, err)
	}
	if added, updated, err := seed.Seed(ctx, db, log); err != nil || added != 0 || updated != 0 {
		t.Fatalf("second seed: %d added, %d updated, %v", added, updated, err)
	}

	q := store.New(db)
	byID := func(id string) store.Egg {
		t.Helper()
		e, err := q.GetEggByCatalogID(ctx, pgtype.Text{String: id, Valid: true})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	var paper catalog.Entry
	for _, e := range all {
		if e.ID == "minecraft/paper" {
			paper = e
		}
	}
	p := byID("minecraft/paper")
	file, _ := paper.ServerEgg()
	if p.OrgID.Valid || p.Status != "published" || p.Origin != "catalog" || !p.Certified || p.Category != "minecraft" ||
		!bytes.Equal(p.Egg, file) || p.Name != "Paper" {
		t.Fatalf("paper: %+v", p)
	}

	// The catalog changed (as an older Panel seeded it): the seed follows,
	// as a new version.
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE eggs SET sha256 = 'old' WHERE id = $1`, p.ID)
	if _, updated, err := seed.Seed(ctx, db, log); err != nil || updated != 1 {
		t.Fatalf("after a catalog change: %d updated, %v", updated, err)
	}
	if p := byID("minecraft/paper"); p.Version != 2 || !bytes.Equal(p.Egg, file) {
		t.Errorf("updated paper: version %d", p.Version)
	}

	// Staff edited it: theirs from then on.
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by) VALUES ($1, 3, 'x', 'edited', 'Edited', 'admin@raptor.example')`, p.ID)
	exec(`UPDATE eggs SET sha256 = 'edited', version = 3, certified = false WHERE id = $1`, p.ID)
	if _, updated, err := seed.Seed(ctx, db, log); err != nil || updated != 0 {
		t.Fatalf("after a staff edit: %d updated, %v", updated, err)
	}
	if p := byID("minecraft/paper"); p.Sha256 != "edited" || p.Certified {
		t.Errorf("staff's edit was overwritten: %+v", p)
	}
}
