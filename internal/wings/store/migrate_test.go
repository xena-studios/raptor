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

// Migration 25 rebuilds database_instances, which databases and instance
// addresses refer to with ON DELETE CASCADE: none may be lost.
func TestDatabaseEnginesMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO networks (id, name, subnet, created_at, updated_at) VALUES ('n1', 'A', '172.27.0.0/16', 1, 1)`,
		`INSERT INTO database_instances (id, name, engine, version, image, root_password, networks, limits, settings, created_at, updated_at, external_port, prev_version)
			VALUES ('i1', 'MariaDB', 'mariadb', '11.8', 'img', X'01', '["n1"]', '{}', '{}', 1, 1, 13306, '11.4')`,
		`INSERT INTO instance_addresses (instance_id, network_id, ip) VALUES ('i1', 'n1', '172.27.128.2')`,
		`INSERT INTO databases (id, instance_id, server_id, label, db_name, username, password, created_at, ro_username, ro_password)
			VALUES ('d1', 'i1', 's1', 'luckperms', 'ss1_luckperms', 'ss1_luckperms', X'02', 1, 'rs1_luckperms', X'03')`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO database_instances (id, name, engine, version, image, root_password, limits, settings, created_at, updated_at)
		VALUES ('i2', 'Redis', 'redis', '8', 'img', X'01', '{}', '{}', 1, 1)`); err == nil {
		t.Fatal("version 24 accepted a Redis instance")
	}
	_ = raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	in, err := db.Read.GetInstance(ctx, "i1")
	if err != nil || in.ExternalPort != 13306 || in.PrevVersion != "11.4" || in.Networks != `["n1"]` {
		t.Fatalf("instance after migrating: %+v, %v", in, err)
	}
	d, err := db.Read.GetDatabase(ctx, "d1")
	if err != nil || d.RoUsername != "rs1_luckperms" || string(d.RoPassword) != "\x03" {
		t.Fatalf("database after migrating: %+v, %v", d, err)
	}
	if a, err := db.Read.ListInstanceAddresses(ctx); err != nil || len(a) != 1 {
		t.Fatalf("addresses after migrating: %+v, %v", a, err)
	}
	if err := db.Write.InsertInstance(ctx, InsertInstanceParams{
		ID: "i2", Name: "Redis", Engine: "redis", Version: "8", Image: "img",
		RootPassword: []byte{1}, Networks: "[]", Limits: "{}", Settings: "{}", DesiredState: "running", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("a Redis instance: %v", err)
	}
	// The references still cascade.
	if err := db.Write.DeleteInstance(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Read.GetDatabase(ctx, "d1"); err == nil {
		t.Error("deleting the instance left its database")
	}
	if a, _ := db.Read.ListInstanceAddresses(ctx); len(a) != 0 {
		t.Error("deleting the instance left its address")
	}
}

// Migration 28 rebuilds database_instances again, for Valkey: nothing may
// be lost, and the references still cascade.
func TestValkeyMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO networks (id, name, subnet, created_at, updated_at) VALUES ('n1', 'A', '172.27.0.0/16', 1, 1)`,
		`INSERT INTO database_instances (id, name, engine, version, image, root_password, networks, limits, settings, created_at, updated_at, external_port)
			VALUES ('i1', 'Redis', 'redis', '8.10', 'img', X'01', '["n1"]', '{}', '{"advanced":"hz = 20"}', 1, 1, 16379)`,
		`INSERT INTO instance_addresses (instance_id, network_id, ip) VALUES ('i1', 'n1', '172.27.128.2')`,
		`INSERT INTO databases (id, instance_id, server_id, label, db_name, username, password, created_at, viewer_username, viewer_password)
			VALUES ('d1', 'i1', 's1', 'cache', 'ss1_cache', 'ss1_cache', X'02', 1, 'vss1_cache', X'03')`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO database_instances (id, name, engine, version, image, root_password, limits, settings, created_at, updated_at)
		VALUES ('i2', 'Valkey', 'valkey', '9.1', 'img', X'01', '{}', '{}', 1, 1)`); err == nil {
		t.Fatal("version 27 accepted a Valkey instance")
	}
	_ = raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	in, err := db.Read.GetInstance(ctx, "i1")
	if err != nil || in.ExternalPort != 16379 || in.Settings != `{"advanced":"hz = 20"}` {
		t.Fatalf("instance after migrating: %+v, %v", in, err)
	}
	if d, err := db.Read.GetDatabase(ctx, "d1"); err != nil || d.ViewerUsername != "vss1_cache" || len(d.ViewerPassword) != 1 {
		t.Fatalf("database after migrating: %+v, %v", d, err)
	}
	if err := db.Write.InsertInstance(ctx, InsertInstanceParams{
		ID: "i2", Name: "Valkey", Engine: "valkey", Version: "9.1", Image: "img",
		RootPassword: []byte{1}, Networks: "[]", Limits: "{}", Settings: "{}", DesiredState: "running", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("a Valkey instance: %v", err)
	}
	if err := db.Write.DeleteInstance(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Read.GetDatabase(ctx, "d1"); err == nil {
		t.Error("deleting the instance left its database")
	}
	if a, _ := db.Read.ListInstanceAddresses(ctx); len(a) != 0 {
		t.Error("deleting the instance left its address")
	}
}
