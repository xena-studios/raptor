-- +goose Up
-- Staged Wings rollouts (docs/WINGS.md#updates): a version goes to 5%, then
-- 25%, then every node, and stops if updates start failing.
CREATE TABLE wings_rollouts (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    version          text        NOT NULL,
    state            text        NOT NULL DEFAULT 'running'
                     CHECK (state IN ('running', 'paused', 'halted', 'done', 'cancelled')),
    stage            int         NOT NULL DEFAULT 0,
    stage_started_at timestamptz NOT NULL DEFAULT now(),
    reason           text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz
);
-- One rollout at a time.
CREATE UNIQUE INDEX wings_rollouts_one_active ON wings_rollouts ((true)) WHERE state IN ('running', 'paused');

CREATE TABLE rollout_nodes (
    rollout_id   uuid        NOT NULL REFERENCES wings_rollouts (id),
    node_id      uuid        NOT NULL REFERENCES nodes (id),
    stage        int         NOT NULL,
    status       text        NOT NULL CHECK (status IN ('sent', 'updated', 'failed', 'skipped')),
    from_version text        NOT NULL,
    error        text        NOT NULL DEFAULT '',
    sent_at      timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    PRIMARY KEY (rollout_id, node_id)
);

-- +goose Down
DROP TABLE rollout_nodes;
DROP TABLE wings_rollouts;
