package store

import (
	"context"
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
