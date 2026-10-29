package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/xena-studios/raptor/db/wings/migrations"
)

// Migration 29 moves each database's server into a link, keeping every
// column, and two servers' databases on one instance can share a label;
// deleting a database or its instance removes its links.
func TestDatabaseLinksMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	p, err := goose.NewProvider(goose.DialectSQLite3, raw, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO database_instances (id, name, engine, version, image, root_password, limits, settings, created_at, updated_at)
			VALUES ('i1', 'MariaDB', 'mariadb', '11.8', 'img', X'01', '{}', '{}', 1, 1)`,
		`INSERT INTO databases (id, instance_id, server_id, label, db_name, username, password, created_at, ro_username, ro_password, viewer_username, viewer_password)
			VALUES ('d1', 'i1', 's1', 'lp', 'ss1_lp', 'ss1_lp', X'02', 5, 'rs1_lp', X'04', 'vs1_lp', X'03')`,
		`INSERT INTO databases (id, instance_id, server_id, label, db_name, username, password, created_at)
			VALUES ('d2', 'i1', 's2', 'lp', 'ss2_lp', 'ss2_lp', X'02', 6)`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	d, err := db.Read.GetDatabase(ctx, "d1")
	if err != nil || d.RoUsername != "rs1_lp" || len(d.RoPassword) != 1 || d.ViewerUsername != "vs1_lp" || len(d.ViewerPassword) != 1 || d.CreatedAt != 5 {
		t.Fatalf("database after migrating: %+v, %v", d, err)
	}
	links, err := db.Read.ListDatabaseLinks(ctx)
	if err != nil || len(links) != 2 || links[0] != (DatabaseLink{DatabaseID: "d1", ServerID: "s1", CreatedAt: 5}) || links[1].ServerID != "s2" {
		t.Fatalf("links: %+v, %v", links, err)
	}
	if mine, err := db.Read.ListServerDatabases(ctx, "s1"); err != nil || len(mine) != 1 || mine[0].ID != "d1" {
		t.Fatalf("s1's databases: %+v, %v", mine, err)
	}
	// One database, two servers; a server's links go without the database.
	if err := db.Write.InsertDatabaseLink(ctx, InsertDatabaseLinkParams{DatabaseID: "d1", ServerID: "s2", CreatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write.DeleteServerLinks(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Read.GetDatabase(ctx, "d1"); err != nil {
		t.Fatalf("unlinking deleted the database: %v", err)
	}
	if mine, _ := db.Read.ListServerDatabases(ctx, "s2"); len(mine) != 2 || mine[0].ID != "d2" {
		t.Fatalf("s2's databases, in link order: %+v", mine)
	}
	if err := db.Write.DeleteDatabase(ctx, "d2"); err != nil {
		t.Fatal(err)
	}
	if err := db.Write.DeleteInstance(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := db.Read.ListDatabaseLinks(ctx); len(l) != 0 {
		t.Errorf("links left after their databases went: %+v", l)
	}
}
