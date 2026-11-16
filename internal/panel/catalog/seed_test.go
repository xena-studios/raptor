package catalog_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	seed "github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/library"
)

const testEgg = "generic/raptor-test"

// Seeding puts every library egg in the official library once; eggs only
// the seed changed follow the library, and eggs staff changed don't.
func TestSeed(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	all, err := library.All()
	if err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err := seed.Seed(ctx, db, log)
	if err != nil || added != len(all) || updated != 0 || removed != 0 {
		t.Fatalf("first seed: %d added, %d updated, %d removed, %v", added, updated, removed, err)
	}
	// Nothing changed: nothing is written, and it's quick (no icons
	// decoded, no rows touched).
	var before time.Time
	if err := db.QueryRow(ctx, `SELECT max(updated_at) FROM eggs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if added, updated, removed, err := seed.Seed(ctx, db, log); err != nil || added != 0 || updated != 0 || removed != 0 {
		t.Fatalf("second seed: %d added, %d updated, %d removed, %v", added, updated, removed, err)
	}
	t.Logf("re-seeding %d unchanged eggs took %v", len(all), time.Since(start))
	var after time.Time
	if err := db.QueryRow(ctx, `SELECT max(updated_at) FROM eggs`).Scan(&after); err != nil || !after.Equal(before) {
		t.Errorf("the second seed wrote: %v → %v, %v", before, after, err)
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
	var entry library.Entry
	for _, e := range all {
		if e.ID == testEgg {
			entry = e
		}
	}
	p := byID(testEgg)
	file, _ := entry.ServerEgg()
	if p.OrgID.Valid || p.Status != "published" || p.Origin != "catalog" || !p.Certified || p.Category != "generic" ||
		!bytes.Equal(p.Egg, file) || p.Name != "Raptor test server" || p.SourceUrl != entry.SourceURL() ||
		p.IconSource != "file" || !p.Icon.Valid || !p.Internal {
		t.Fatalf("the test egg: %+v", p)
	}
	if paper := byID("minecraft/paper"); paper.Internal || paper.Category != "minecraft" || !paper.Icon.Valid {
		t.Errorf("Paper: %+v", paper)
	}

	// The internal test egg isn't in an org's library; staff see it.
	var org pgtype.UUID
	if err := db.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ('Acme') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	listed, err := q.ListLibraryEggs(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	official, err := q.ListOfficialEggs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(all)-1 || len(official) != len(all) {
		t.Errorf("an org lists %d eggs, staff %d, of %d", len(listed), len(official), len(all))
	}
	for _, r := range listed {
		if r.Egg.ID == p.ID {
			t.Error("an org lists the internal test egg")
		}
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The library changed (as an older Panel seeded it): the seed follows,
	// as a new version.
	exec(`UPDATE eggs SET sha256 = 'old', library_sum = '' WHERE id = $1`, p.ID)
	if _, updated, _, err := seed.Seed(ctx, db, log); err != nil || updated != 1 {
		t.Fatalf("after a library change: %d updated, %v", updated, err)
	}
	if p := byID(testEgg); p.Version != 2 || !bytes.Equal(p.Egg, file) {
		t.Errorf("updated: version %d", p.Version)
	}
	// Only the icon or a setting changed: no new version.
	exec(`UPDATE eggs SET library_sum = 'old', internal = false WHERE id = $1`, p.ID)
	if _, updated, _, err := seed.Seed(ctx, db, log); err != nil || updated != 0 {
		t.Fatalf("after a settings change: %d updated, %v", updated, err)
	}
	if p := byID(testEgg); p.Version != 2 || !p.Internal {
		t.Errorf("settings: version %d, internal %v", p.Version, p.Internal)
	}

	// Staff edited it: theirs from then on.
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by) VALUES ($1, 3, 'x', 'edited', 'Edited', 'admin@raptor.example')`, p.ID)
	exec(`UPDATE eggs SET sha256 = 'edited', version = 3, certified = false, library_sum = '' WHERE id = $1`, p.ID)
	if _, updated, _, err := seed.Seed(ctx, db, log); err != nil || updated != 0 {
		t.Fatalf("after a staff edit: %d updated, %v", updated, err)
	}
	if p := byID(testEgg); p.Sha256 != "edited" || p.Certified {
		t.Errorf("staff's edit was overwritten: %+v", p)
	}
	// Staff put the library's file back: it follows the library again.
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by) VALUES ($1, 4, $2, $3, 'Back', 'admin@raptor.example')`, p.ID, file, byID("generic/raptor-test").Sha256)
	exec(`UPDATE eggs SET sha256 = (SELECT sha256 FROM egg_versions WHERE egg_id = $1 AND version = 2), version = 4 WHERE id = $1`, p.ID)
	if _, _, _, err := seed.Seed(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if p := byID(testEgg); !p.Certified || p.LibrarySum == "" {
		t.Errorf("back to the library's file, but not following it: %+v", p)
	}
}

// An egg imported by hand from the egg library (before the Panel seeded
// it) follows the library; one staff edited after importing it doesn't.
func TestSeedAdoptsImports(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(name, libraryID, note string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO eggs (org_id, name, category, origin, status, egg, sha256, catalog_id)
			VALUES (NULL, $1, 'other', 'import', 'published', '{}', 'imported', $2) RETURNING id`, name, libraryID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by) VALUES ($1, 1, '{}', 'imported', $2, 'admin@raptor.example')`, id, note)
		return id
	}
	imported := insert("Valheim", "steam/valheim", "Imported")
	edited := insert("Rust", "steam/rust", "My changes")
	if _, _, _, err := seed.Seed(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	var origin, sha string
	var version int
	if err := db.QueryRow(ctx, `SELECT origin, sha256, version FROM eggs WHERE id = $1`, imported).Scan(&origin, &sha, &version); err != nil ||
		origin != "catalog" || sha == "imported" || version != 2 {
		t.Errorf("the imported egg: %s %s v%d, %v", origin, sha, version, err)
	}
	if err := db.QueryRow(ctx, `SELECT origin, sha256, version FROM eggs WHERE id = $1`, edited).Scan(&origin, &sha, &version); err != nil ||
		origin != "import" || sha != "imported" || version != 1 {
		t.Errorf("the edited egg: %s %s v%d, %v", origin, sha, version, err)
	}
}

// Eggs the library no longer has are deleted, unless a server was made
// from one: those stay as they are. Staff's own official eggs (not from
// the library) are never touched.
func TestSeedRetires(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(name, catalogID, origin string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO eggs (org_id, name, category, origin, status, egg, sha256, catalog_id)
			VALUES (NULL, $1, 'minecraft', $3, 'published', '{}', 'x', nullif($2, '')) RETURNING id`, name, catalogID, origin).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, created_by) VALUES ($1, 1, '{}', 'x', 'Raptor')`, id)
		return id
	}
	used := insert("Used", "retired/used", "catalog")
	oldUsed := insert("Old used", "retired/old-used", "catalog")
	unused := insert("Unused", "retired/unused", "catalog")
	staffs := insert("Staff's own", "", "scratch")
	// Ones the seed doesn't own: a retired egg staff gave a new version,
	// and an egg imported with a library ID the library doesn't have.
	adopted := insert("Adopted", "retired/adopted", "catalog")
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, created_by) VALUES ($1, 2, '{}', 'y', 'admin@raptor.example')`, adopted)
	exec(`UPDATE eggs SET sha256 = 'y', version = 2 WHERE id = $1`, adopted)
	imported := insert("Imported", "retired/imported", "import")

	var org, node string
	if err := db.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ('Acme') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	// One is used by its library row's ID, the other by its catalog ID (a
	// server from before the catalog was in the library).
	exec(`INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, egg_source)
		VALUES ($1, 's1', 'smp', 1, now(), now(), $2), ($1, 's2', 'old', 1, now(), now(), 'retired/old-used')`, node, used)

	_, _, removed, err := seed.Seed(ctx, db, log)
	if err != nil || removed != 1 {
		t.Fatalf("seed: %d removed, %v", removed, err)
	}
	left := map[string]bool{}
	rows, _ := db.Query(ctx, `SELECT id::text FROM eggs`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		left[id] = true
	}
	if !left[adopted] || !left[imported] {
		t.Errorf("the seed deleted an egg it doesn't own: adopted %v, imported %v", left[adopted], left[imported])
	}
	if !left[used] || !left[oldUsed] || left[unused] || !left[staffs] {
		t.Errorf("used %v, old used %v, unused %v, staff's %v", left[used], left[oldUsed], left[unused], left[staffs])
	}
	var name string
	if err := db.QueryRow(ctx, `SELECT name FROM eggs WHERE id = $1`, used).Scan(&name); err != nil || name != "Used" {
		t.Errorf("an egg in use was changed: %q %v", name, err)
	}
}
