package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/xena-studios/raptor/db/wings/migrations"
)

// Migration 9 rebuilds the backups table to allow final backups; backups
// made before it must come through unchanged.
func TestFinalBackupsMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, raw, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO backups (id, server_id, destination_id, kind, status, snapshot_id, size, created_at, expires_at)
		VALUES ('b1', 's1', 'local', 'safety', 'ok', 'snap', 42, 1000, 2000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO backups (id, server_id, destination_id, kind, created_at) VALUES ('b2', 's1', 'local', 'final', 1)`); err == nil {
		t.Fatal("version 8 accepted a final backup")
	}
	_ = raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	b, err := db.Read.GetBackup(ctx, "b1")
	if err != nil || b.Kind != "safety" || b.SnapshotID != "snap" || b.Size != 42 || b.ExpiresAt.Int64 != 2000 {
		t.Fatalf("backup after migrating: %+v, %v", b, err)
	}
	if err := db.Write.InsertBackup(ctx, InsertBackupParams{ID: "b2", ServerID: "s1", DestinationID: "local", Kind: "final", CreatedAt: 1}); err != nil {
		t.Fatalf("final backup: %v", err)
	}
	if got, err := db.Read.GetJobBackup(ctx, GetJobBackupParams{JobID: "", Kind: "final"}); err != nil || got.ID != "b2" {
		t.Fatalf("job backup: %+v, %v", got, err)
	}
}

// Migration 13 rebuilds backup_destinations, which backups refer to with ON
// DELETE CASCADE: no backup may be lost, and settings become targets.
func TestBackupTargetsMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO backup_destinations (id, name, type, config, created_at, updated_at) VALUES ('d1', 'B2', 's3', '{"bucket": "b"}', 1, 1)`,
		`INSERT INTO servers (id, name, egg, egg_hash, image, startup, created_at, updated_at) VALUES ('s1', 'mc', X'7B7D', 'h', 'img', 'run', 1, 1)`,
		`INSERT INTO backup_policies (server_id, destination_id, keep_last, keep_daily, keep_weekly, keep_monthly, updated_at) VALUES ('s1', 'd1', 5, 6, 7, 8, 1)`,
		`INSERT INTO backups (id, server_id, destination_id, kind, status, snapshot_id, created_at) VALUES ('b1', 's1', 'd1', 'manual', 'ok', 'snap1', 1)`,
		`INSERT INTO backups (id, server_id, destination_id, kind, status, snapshot_id, created_at) VALUES ('b2', 's1', 'local', 'manual', 'ok', 'snap2', 2)`,
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
	all, err := db.Read.ListBackups(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("backups after migrating: %+v, %v", all, err)
	}
	d, err := db.Read.GetBackupDestination(ctx, "d1")
	if err != nil || d.Config != `{"bucket": "b"}` || d.Type != "s3" || d.Size.Valid {
		t.Fatalf("destination after migrating: %+v, %v", d, err)
	}
	targets, err := db.Read.ListBackupTargets(ctx, "s1")
	if err != nil || len(targets) != 1 || targets[0].DestinationID != "d1" || targets[0].KeepLast != 5 || targets[0].KeepMonthly != 8 {
		t.Fatalf("targets after migrating: %+v, %v", targets, err)
	}
	// Types are checked in Go now.
	if err := db.Write.InsertBackupDestination(ctx, InsertBackupDestinationParams{ID: "d2", Name: "Box", Type: "sftp", Config: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("an sftp destination: %v", err)
	}
	// Foreign keys are back on: deleting a destination still takes its backups.
	if err := db.Write.DeleteBackupDestination(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if all, _ := db.Read.ListBackups(ctx, ""); len(all) != 1 {
		t.Fatalf("foreign keys off after the migration: %d backups", len(all))
	}
}
