package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seed(t *testing.T, path string, value string) *DB {
	t.Helper()
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write.SetKV(context.Background(), SetKVParams{Key: "k", Value: []byte(value)}); err != nil {
		t.Fatal(err)
	}
	return db
}

func corrupt(t *testing.T, path string, offset int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	junk := []byte(strings.Repeat("\xde\xad\xbe\xef", 1024))
	if _, err := f.WriteAt(junk, offset); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// A damaged state database is replaced by its newest good snapshot, and the
// damaged one is kept aside.
func TestRecoverFromSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, offset := range map[string]int64{"header": 0, "pages": 4096} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db := seed(t, path, "snapshotted")
			if _, err := db.Snapshot(ctx, "hourly", 3); err != nil {
				t.Fatal(err)
			}
			// A later change the snapshot doesn't have; then damage.
			_ = db.Write.SetKV(ctx, SetKVParams{Key: "k", Value: []byte("lost")})
			_ = db.Close()
			corrupt(t, path, offset)

			db, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = db.Close() }()
			r := db.Recovered
			if r == nil || !strings.Contains(r.Snapshot, "hourly-") || r.Problem == "" {
				t.Fatalf("recovered: %+v", r)
			}
			v, err := db.Read.GetKV(ctx, "k")
			if err != nil || string(v) != "snapshotted" {
				t.Errorf("value after recovery: %q, %v", v, err)
			}
			if _, err := os.Stat(r.Corrupt); err != nil {
				t.Errorf("the damaged database wasn't kept: %v", err)
			}
		})
	}
}

func TestRecoverHealthyAndNoSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db := seed(t, path, "v")
	_ = db.Close()
	db, err := Open(ctx, path)
	if err != nil || db.Recovered != nil {
		t.Fatalf("healthy: %+v, %v", db, err)
	}
	_ = db.Close()

	// Damaged, and no snapshot: refuse rather than start empty.
	corrupt(t, path, 0)
	if _, err := Open(ctx, path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("no snapshot: %v", err)
	}
	// A damaged snapshot isn't used either.
	_ = os.MkdirAll(SnapshotDir(path), 0o700)
	_ = os.WriteFile(filepath.Join(SnapshotDir(path), "hourly-20260101T000000.000000000Z.db"), []byte("not a database at all, really"), 0o600)
	if _, err := Open(ctx, path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("only a bad snapshot: %v", err)
	}
}
