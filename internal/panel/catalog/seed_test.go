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

const testEgg = "generic/raptor-test"

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
	added, updated, removed, err := seed.Seed(ctx, db, log)
	if err != nil || added != len(all) || updated != 0 || removed != 0 {
		t.Fatalf("first seed: %d added, %d updated, %d removed, %v", added, updated, removed, err)
	}
	if added, updated, removed, err := seed.Seed(ctx, db, log); err != nil || added != 0 || updated != 0 || removed != 0 {
		t.Fatalf("second seed: %d added, %d updated, %d removed, %v", added, updated, removed, err)
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
	var entry catalog.Entry
	for _, e := range all {
		if e.ID == testEgg {
			entry = e
		}
	}
	p := byID(testEgg)
	file, _ := entry.ServerEgg()
	if p.OrgID.Valid || p.Status != "published" || p.Origin != "catalog" || !p.Certified || p.Category != "generic" ||
		!bytes.Equal(p.Egg, file) || p.Name != "Raptor test server" || p.SourceUrl != entry.SourceURL() ||
		p.IconSource != "file" || !p.Icon.Valid {
		t.Fatalf("the test egg: %+v", p)
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
	if _, updated, _, err := seed.Seed(ctx, db, log); err != nil || updated != 1 {
		t.Fatalf("after a catalog change: %d updated, %v", updated, err)
	}
	if p := byID(testEgg); p.Version != 2 || !bytes.Equal(p.Egg, file) {
		t.Errorf("updated: version %d", p.Version)
	}

	// Staff edited it: theirs from then on.
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by) VALUES ($1, 3, 'x', 'edited', 'Edited', 'admin@raptor.example')`, p.ID)
	exec(`UPDATE eggs SET sha256 = 'edited', version = 3, certified = false WHERE id = $1`, p.ID)
	if _, updated, _, err := seed.Seed(ctx, db, log); err != nil || updated != 0 {
		t.Fatalf("after a staff edit: %d updated, %v", updated, err)
	}
	if p := byID(testEgg); p.Sha256 != "edited" || p.Certified {
		t.Errorf("staff's edit was overwritten: %+v", p)
	}
}

// Eggs the catalog no longer has are deleted, unless a server was made
// from one: those stay as they are. Staff's own official eggs (not from
// the catalog) are never touched.
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
	paper := insert("Paper", "minecraft/paper", "catalog")
	vanilla := insert("Vanilla", "minecraft/vanilla", "catalog")
	rust := insert("Rust", "steam/rust", "catalog")
	staffs := insert("Staff's own", "", "scratch")
	// Ones the seed doesn't own: a retired catalog egg staff gave a new
	// version (from the egg library), and an egg imported from the library.
	adopted := insert("Fabric", "minecraft/fabric", "catalog")
	exec(`INSERT INTO egg_versions (egg_id, version, egg, sha256, created_by) VALUES ($1, 2, '{}', 'y', 'admin@raptor.example')`, adopted)
	imported := insert("Valheim", "steam/valheim", "import")

	var org, node string
	if err := db.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ('Acme') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	// Paper is used by its library ID, Vanilla by its catalog ID (a server
	// from before the catalog was in the library).
	exec(`INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, egg_source)
		VALUES ($1, 's1', 'smp', 1, now(), now(), $2), ($1, 's2', 'old', 1, now(), now(), 'minecraft/vanilla')`, node, paper)

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
	if !left[paper] || !left[vanilla] || left[rust] || !left[staffs] {
		t.Errorf("left %v: paper %v, vanilla %v, rust %v, staff's %v", left, left[paper], left[vanilla], left[rust], left[staffs])
	}
	var name string
	if err := db.QueryRow(ctx, `SELECT name FROM eggs WHERE id = $1`, paper).Scan(&name); err != nil || name != "Paper" {
		t.Errorf("an egg in use was changed: %q %v", name, err)
	}
}
