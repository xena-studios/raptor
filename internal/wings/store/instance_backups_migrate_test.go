package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/xena-studios/raptor/db/wings/migrations"
)

// Migration 30: existing backups are servers', instances' are listed apart
// from them, and an instance's backup settings go with the instance.
func TestInstanceBackupsMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO backups (id, server_id, destination_id, kind, created_at) VALUES ('b1', 's1', 'local', 'manual', 1)`,
		`INSERT INTO database_instances (id, name, engine, version, image, root_password, limits, settings, created_at, updated_at)
			VALUES ('i1', 'MariaDB', 'mariadb', '11.8', 'img', X'01', '{}', '{}', 1, 1)`,
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
	if b, err := db.Read.GetBackup(ctx, "b1"); err != nil || b.Subject != "server" {
		t.Fatalf("an existing backup: %+v, %v", b, err)
	}
	if err := db.Write.InsertInstanceBackup(ctx, InsertInstanceBackupParams{ID: "b2", ServerID: "i1", DestinationID: "local", Kind: "manual", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if all, err := db.Read.ListBackups(ctx, ""); err != nil || len(all) != 1 || all[0].ID != "b1" {
		t.Fatalf("servers' backups: %+v, %v", all, err)
	}
	if mine, err := db.Read.ListInstanceBackups(ctx, "i1"); err != nil || len(mine) != 1 || mine[0].ID != "b2" {
		t.Fatalf("the instance's backups: %+v, %v", mine, err)
	}
	if err := db.Write.UpsertInstanceBackupPolicy(ctx, UpsertInstanceBackupPolicyParams{
		InstanceID: "i1", Enabled: 1, At: "04:00", KeepLast: 7, DestinationID: "local", UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write.DeleteInstance(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Read.GetInstanceBackupPolicy(ctx, "i1"); err == nil {
		t.Error("a deleted instance's backup settings are still there")
	}
	// Its backups outlive it, like a deleted server's.
	if mine, _ := db.Read.ListInstanceBackups(ctx, "i1"); len(mine) != 1 {
		t.Errorf("a deleted instance's backups: %+v", mine)
	}
}
