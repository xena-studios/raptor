-- +goose Up
-- A schedule's runs, from the node's schedule.run.* events (docs/PANEL.md
-- #schedule-runs). Kept on the Panel: Wings drops its events after a week.
-- run_key is the run's job ID, or skip:<event seq> for a skipped run.
CREATE TABLE schedule_runs (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id       uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    server_id     text        NOT NULL,
    schedule_id   text        NOT NULL,
    run_key       text        NOT NULL,
    -- running, succeeded, failed, or skipped.
    status        text        NOT NULL,
    -- scheduled, manual, or missed (late, ran once).
    reason        text        NOT NULL,
    -- Why a run was skipped: missed, offline, or still_running.
    skip_reason   text        NOT NULL DEFAULT '',
    -- [{"type", "ok", "error"}], one per step that ran.
    steps         jsonb       NOT NULL DEFAULT '[]',
    error         text        NOT NULL DEFAULT '',
    scheduled_for timestamptz,
    started_at    timestamptz NOT NULL,
    finished_at   timestamptz,
    UNIQUE (node_id, run_key)
);
CREATE INDEX schedule_runs_server ON schedule_runs (node_id, server_id, id DESC);
CREATE INDEX schedule_runs_schedule ON schedule_runs (node_id, schedule_id, id DESC);

ALTER TABLE schedule_runs ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON schedule_runs TO raptor_app;
CREATE POLICY schedule_runs_read ON schedule_runs FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

-- +goose Down
DROP TABLE schedule_runs;
