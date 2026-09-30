-- +goose Up
-- Final backups, taken before a server is deleted (docs/SERVERS.md#deleting-a-server).
-- SQLite can't change a CHECK constraint, so the table is rebuilt.
CREATE TABLE backups_new (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'safety', 'final')),
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
    expires_at     INTEGER           -- unix ms; safety and local final backups
) STRICT;

INSERT INTO backups_new SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX backups_server ON backups (server_id, created_at);
CREATE INDEX backups_destination ON backups (destination_id);
CREATE INDEX backups_job ON backups (job_id);

-- +goose Down
DELETE FROM backups WHERE kind = 'final';
CREATE TABLE backups_old (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    destination_id TEXT NOT NULL REFERENCES backup_destinations (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'safety')),
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
