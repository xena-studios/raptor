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
