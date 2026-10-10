-- +goose Up
-- Recovering backups (docs/WINGS.md#recovering-backups): a destination can
-- hold another node's backups, read with that node's key, or this node's
-- as they were at a point in time. Such destinations are read-only.
ALTER TABLE backup_destinations ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1));
-- The key of the backups there, when it isn't this node's.
ALTER TABLE backup_destinations ADD COLUMN repo_password TEXT NOT NULL DEFAULT '';
-- Read the storage as it was then (unix ms; versioned S3 storage only).
ALTER TABLE backup_destinations ADD COLUMN point_in_time INTEGER;

-- Backups found there are 'recovered'. SQLite can't change a CHECK, so the
-- table is rebuilt (nothing refers to it).
CREATE TABLE backups_new (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'safety', 'final', 'recovered')),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'ok', 'failed')),
    locked         INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    snapshot_id    TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    files          INTEGER NOT NULL DEFAULT 0,
    uploaded       INTEGER NOT NULL DEFAULT 0,
    warning        TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    job_id         TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    finished_at    INTEGER,
    expires_at     INTEGER
) STRICT;
INSERT INTO backups_new SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX backups_server ON backups (server_id, created_at);
CREATE INDEX backups_destination ON backups (destination_id);
CREATE INDEX backups_job ON backups (job_id);

-- +goose Down
DELETE FROM backups WHERE kind = 'recovered';
CREATE TABLE backups_old (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'safety', 'final')),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'ok', 'failed')),
    locked         INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    snapshot_id    TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    files          INTEGER NOT NULL DEFAULT 0,
    uploaded       INTEGER NOT NULL DEFAULT 0,
    warning        TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    job_id         TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    finished_at    INTEGER,
    expires_at     INTEGER
) STRICT;
INSERT INTO backups_old SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_old RENAME TO backups;
CREATE INDEX backups_server ON backups (server_id, created_at);
CREATE INDEX backups_destination ON backups (destination_id);
CREATE INDEX backups_job ON backups (job_id);
ALTER TABLE backup_destinations DROP COLUMN point_in_time;
ALTER TABLE backup_destinations DROP COLUMN repo_password;
ALTER TABLE backup_destinations DROP COLUMN read_only;
