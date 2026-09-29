-- +goose Up
-- Schedules (docs/WINGS.md#scheduler). Steps are stored as JSON: they're
-- always read and replaced together.
CREATE TABLE schedules (
    id               TEXT PRIMARY KEY,
    server_id        TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    cron             TEXT NOT NULL,
    timezone         TEXT NOT NULL DEFAULT 'UTC',
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    only_when_online INTEGER NOT NULL DEFAULT 0 CHECK (only_when_online IN (0, 1)),
    jitter_s         INTEGER NOT NULL DEFAULT 0,
    missed           TEXT NOT NULL DEFAULT 'skip' CHECK (missed IN ('skip', 'run_once')),
    steps            TEXT NOT NULL DEFAULT '[]', -- JSON list
    next_run_at      INTEGER, -- unix ms, jitter included; NULL when disabled
    last_run_at      INTEGER, -- unix ms
    version          INTEGER NOT NULL DEFAULT 1,
    created_at       INTEGER NOT NULL, -- unix ms
    updated_at       INTEGER NOT NULL  -- unix ms
) STRICT;

CREATE INDEX schedules_server ON schedules (server_id);
CREATE INDEX schedules_due ON schedules (next_run_at) WHERE next_run_at IS NOT NULL;

-- Progress of a running job, so one interrupted by a Wings stop resumes
-- where it was (a schedule run continues after its last finished step).
ALTER TABLE jobs ADD COLUMN checkpoint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN checkpoint;
DROP TABLE schedules;
