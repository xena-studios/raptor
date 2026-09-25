package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndKV(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write.SetKV(ctx, SetKVParams{Key: "node_id", Value: []byte("abc")}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(f), perm)
		}
	}
	got, err := db.Read.GetKV(ctx, "node_id")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Fatalf("value = %q, want %q", got, "abc")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must be idempotent (migrations already applied).
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := db.Read.SetKV(ctx, SetKVParams{Key: "x", Value: []byte("y")}); err == nil {
		t.Fatal("write through reader succeeded, want query_only error")
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Write.SetKV(ctx, SetKVParams{Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	var last string
	for range 4 {
		if last, err = db.Snapshot(ctx, "hourly", 2); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(SnapshotDir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("kept %d snapshots, want 2", len(entries))
	}

	// The snapshot is a complete, openable database.
	snap, err := Open(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Close() }()
	v, err := snap.Read.GetKV(ctx, "k")
	if err != nil || string(v) != "v" {
		t.Fatalf("snapshot value = %q, %v", v, err)
	}
}
