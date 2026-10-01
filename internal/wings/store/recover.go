package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrCorrupt means the state database is damaged and no snapshot could
// replace it.
var ErrCorrupt = errors.New("the state database is corrupt")

// Recovery describes a damaged state database replaced by a snapshot.
type Recovery struct {
	Snapshot string // the snapshot restored
	Corrupt  string // where the damaged database was moved
	Problem  string // what the integrity check said
}

// checkFile reports what's wrong with a database file ("" = nothing).
func checkFile(ctx context.Context, path string) string {
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err.Error()
	}
	defer func() { _ = conn.Close() }()
	var res string
	if err := conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		return err.Error()
	}
	if res != "ok" {
		return res
	}
	return ""
}

// recoverIfCorrupt checks the database at path and, if it's damaged, moves
// it (with its WAL) aside and restores the newest snapshot that passes the
// check. A database that doesn't exist yet is fine. Without a usable
// snapshot it fails: starting with an empty database would forget every
// server while their containers keep running.
func recoverIfCorrupt(ctx context.Context, path string) (*Recovery, error) {
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		return nil, nil //nolint:nilerr // new: created by Open
	}
	problem := checkFile(ctx, path)
	if problem == "" {
		return nil, nil
	}
	snaps, _ := filepath.Glob(filepath.Join(SnapshotDir(path), "*.db"))
	// Newest first, by the time in the name (prefix-<time>.db).
	slices.SortFunc(snaps, func(a, b string) int { return strings.Compare(snapTime(b), snapTime(a)) })
	var good string
	for _, s := range snaps {
		if checkFile(ctx, s) == "" {
			good = s
			break
		}
	}
	if good == "" {
		return nil, fmt.Errorf("%w (%s) and no snapshot in %s passes an integrity check; see docs/RELIABILITY.md#state-durability", ErrCorrupt, problem, SnapshotDir(path))
	}
	aside := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(path, aside); err != nil {
		return nil, err
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + ext); err == nil {
			if err := os.Rename(path+ext, aside+ext); err != nil {
				return nil, err
			}
		}
	}
	if err := copyFile(good, path); err != nil {
		return nil, fmt.Errorf("restore %s: %w", good, err)
	}
	return &Recovery{Snapshot: good, Corrupt: aside, Problem: problem}, nil
}

func snapTime(p string) string {
	name := strings.TrimSuffix(filepath.Base(p), ".db")
	if i := strings.LastIndex(name, "-"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a snapshot we wrote
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the state path
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
