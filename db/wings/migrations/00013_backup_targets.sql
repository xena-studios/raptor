-- +goose NO TRANSACTION
-- +goose Up
-- Backups to several destinations per server, every Kopia storage type, and
-- each destination's health (docs/WINGS.md#backups).
--
-- backup_destinations is rebuilt to drop its CHECK on type (SQLite can't
-- change one in place; types are checked in Go now). backups refers to it
-- with ON DELETE CASCADE, so foreign keys are off while it's rebuilt, or
-- dropping the old table would delete every backup. That's a connection
-- setting, which is why this migration runs outside goose's transaction
-- (the writer has a single connection).
PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE backup_destinations_new (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    type          TEXT NOT NULL,
    config        TEXT NOT NULL DEFAULT '{}', -- JSON: the type's settings and credentials
    upload_limit  INTEGER NOT NULL DEFAULT 0, -- bytes per second; 0 = none
    version       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL, -- unix ms
    updated_at    INTEGER NOT NULL, -- unix ms
    -- Health: the last time it worked, the last error and when, and what
    -- it stores (measured at most daily).
    last_ok_at    INTEGER,
    last_error    TEXT NOT NULL DEFAULT '',
    last_error_at INTEGER,
    size          INTEGER,
    size_at       INTEGER
) STRICT;

INSERT INTO backup_destinations_new (id, name, type, config, version, created_at, updated_at)
SELECT id, name, type, config, version, created_at, updated_at FROM backup_destinations;
DROP TABLE backup_destinations;
ALTER TABLE backup_destinations_new RENAME TO backup_destinations;

-- Where a server's backups go: one row per destination, each with its own
-- retention. position orders them; the first is the primary (safety
-- backups go there). No rows = the server uses the defaults.
CREATE TABLE backup_targets (
    server_id      TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id),
    position       INTEGER NOT NULL,
    keep_last      INTEGER NOT NULL,
    keep_daily     INTEGER NOT NULL,
    keep_weekly    INTEGER NOT NULL,
    keep_monthly   INTEGER NOT NULL,
    PRIMARY KEY (server_id, destination_id)
) STRICT;
CREATE INDEX backup_targets_destination ON backup_targets (destination_id);

-- Existing settings become a server's single target. backup_policies keeps
-- its destination and retention columns, written with the primary target,
-- so the previous Wings still reads sensible settings after a rollback.
INSERT INTO backup_targets (server_id, destination_id, position, keep_last, keep_daily, keep_weekly, keep_monthly)
SELECT server_id, destination_id, 0, keep_last, keep_daily, keep_weekly, keep_monthly FROM backup_policies;

COMMIT;

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

BEGIN;

DROP TABLE backup_targets;
DELETE FROM backup_destinations WHERE type NOT IN ('local', 's3');
CREATE TABLE backup_destinations_old (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('local', 's3')),
    config     TEXT NOT NULL DEFAULT '{}',
    version    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
INSERT INTO backup_destinations_old (id, name, type, config, version, created_at, updated_at)
SELECT id, name, type, config, version, created_at, updated_at FROM backup_destinations;
DROP TABLE backup_destinations;
ALTER TABLE backup_destinations_old RENAME TO backup_destinations;

COMMIT;

PRAGMA foreign_keys = ON;
