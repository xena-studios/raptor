-- +goose Up
-- Database instances are backed up on their own (docs/DATABASES.md#backups,
-- DECISIONS #343), through the same destinations, jobs, and retention as
-- servers. An instance backup is a row in backups with subject 'instance'
-- and the instance's ID in server_id (IDs are UUIDs, so they never match a
-- server's).
ALTER TABLE backups ADD COLUMN subject TEXT NOT NULL DEFAULT 'server' CHECK (subject IN ('server', 'instance'));

-- An instance's backup settings: a daily backup at a time of day in the
-- node's time zone, keeping the last keep_last, on one destination. No
-- row = the defaults, written the first time the scheduler sees the
-- instance. last_run_at is when the schedule last queued one.
CREATE TABLE instance_backup_policies (
    instance_id    TEXT    PRIMARY KEY REFERENCES database_instances (id) ON DELETE CASCADE,
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    at             TEXT    NOT NULL DEFAULT '04:00', -- HH:MM
    keep_last      INTEGER NOT NULL DEFAULT 7,
    destination_id TEXT    NOT NULL REFERENCES backup_destinations (id),
    last_run_at    INTEGER NOT NULL DEFAULT 0, -- unix ms
    updated_at     INTEGER NOT NULL            -- unix ms
) STRICT;
CREATE INDEX instance_backup_policies_destination ON instance_backup_policies (destination_id);

-- +goose Down
DROP TABLE instance_backup_policies;
DELETE FROM backups WHERE subject = 'instance';
ALTER TABLE backups DROP COLUMN subject;
