package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/xena-studios/raptor/db/wings/migrations"
)

// DB is the Wings state database. SQLite allows one writer at a time, so writes
// go through a single connection and reads use a separate pool.
type DB struct {
	Write *Queries
	Read  *Queries

	writer *sql.DB
	reader *sql.DB
	path   string
}

// Open opens (creating if needed) the state database at path and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	// SQLite creates database files as 0644 regardless of the umask, and gives
	// its -wal/-shm files the main file's mode. Creating the file first as 0600
	// keeps all of them private.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // path is from config
		_ = f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	writer, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)

	if err := migrate(ctx, writer, path); err != nil {
		_ = writer.Close()
		return nil, err
	}

	reader, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(max(4, runtime.NumCPU()))

	return &DB{
		Write:  New(writer),
		Read:   New(reader),
		writer: writer,
		reader: reader,
		path:   path,
	}, nil
}

// SnapshotDir is where snapshots of the database at path are kept.
func SnapshotDir(path string) string {
	return filepath.Join(filepath.Dir(path), "snapshots")
}

// Snapshot writes a consistent copy of the database with VACUUM INTO and keeps
// only the newest keep snapshots with the same prefix. It returns the new file.
func (db *DB) Snapshot(ctx context.Context, prefix string, keep int) (string, error) {
	return snapshot(ctx, db.writer, db.path, prefix, keep)
}

func snapshot(ctx context.Context, conn *sql.DB, path, prefix string, keep int) (string, error) {
	dir := SnapshotDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, prefix+"-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".db")
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	return dst, prune(dir, prefix, keep)
}

// prune deletes all but the newest keep snapshots with prefix. Names sort by time.
func prune(dir, prefix string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix+"-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// WriteTx runs fn in a write transaction.
func (db *DB) WriteTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(New(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Close closes both connection pools.
func (db *DB) Close() error {
	rerr := db.reader.Close()
	if err := db.writer.Close(); err != nil {
		return err
	}
	return rerr
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// migrate applies pending migrations, taking a snapshot first when an existing
// database is about to change.
func migrate(ctx context.Context, db *sql.DB, path string) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if !pending {
		return nil
	}
	if v, err := p.GetDBVersion(ctx); err == nil && v > 0 {
		if _, err := snapshot(ctx, db, path, "pre-migrate", 5); err != nil {
			return err
		}
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
