package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"runtime"

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
}

// Open opens (creating if needed) the state database at path and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	writer, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)

	if err := migrate(ctx, writer); err != nil {
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
	}, nil
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

func migrate(ctx context.Context, db *sql.DB) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
