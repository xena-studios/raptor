-- +goose Up
-- Backups (docs/WINGS.md#backups).

-- Where backups are stored. 'local' always exists (its path is in the config
-- file). Deleting a destination forgets its backups; their data stays where
-- it is.
CREATE TABLE backup_destinations (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('local', 's3')),
    config     TEXT NOT NULL DEFAULT '{}', -- JSON: the S3 bucket and credentials
    version    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL, -- unix ms
    updated_at INTEGER NOT NULL  -- unix ms
) STRICT;

INSERT INTO backup_destinations (id, name, type, created_at, updated_at)
VALUES ('local', 'Local', 'local', CAST(unixepoch('subsec') * 1000 AS INTEGER), CAST(unixepoch('subsec') * 1000 AS INTEGER));

-- A server's backup settings. No row = the defaults.
CREATE TABLE backup_policies (
    server_id      TEXT PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id),
    keep_last      INTEGER NOT NULL,
    keep_daily     INTEGER NOT NULL,
    keep_weekly    INTEGER NOT NULL,
    keep_monthly   INTEGER NOT NULL,
    ignore         TEXT NOT NULL DEFAULT '[]', -- JSON list of gitignore-style patterns
    version        INTEGER NOT NULL DEFAULT 1,
    updated_at     INTEGER NOT NULL -- unix ms
) STRICT;

-- No foreign key to servers: offsite backups outlive their server.
CREATE TABLE backups (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'safety')),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'ok', 'failed')),
    locked         INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)), -- never deleted by retention
    snapshot_id    TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0, -- total size of the files
    files          INTEGER NOT NULL DEFAULT 0,
    uploaded       INTEGER NOT NULL DEFAULT 0, -- new data written
    warning        TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    job_id         TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL, -- unix ms
    finished_at    INTEGER,          -- unix ms
    expires_at     INTEGER           -- unix ms; safety backups
) STRICT;

CREATE INDEX backups_server ON backups (server_id, created_at);
CREATE INDEX backups_destination ON backups (destination_id);

-- +goose Down
DROP TABLE backups;
DROP TABLE backup_policies;
DROP TABLE backup_destinations;
