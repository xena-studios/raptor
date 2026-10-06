-- +goose Up
-- Schedules and backups in the mirror, replaced with their server's each
-- time it's fetched (and gone with it).
CREATE TABLE m_schedules (
    node_id     uuid        NOT NULL,
    server_id   text        NOT NULL,
    schedule_id text        NOT NULL,
    name        text        NOT NULL,
    enabled     boolean     NOT NULL,
    version     bigint      NOT NULL,
    next_run    timestamptz,
    last_run    timestamptz,
    definition  jsonb       NOT NULL,
    PRIMARY KEY (node_id, schedule_id),
    FOREIGN KEY (node_id, server_id) REFERENCES m_servers (node_id, server_id) ON DELETE CASCADE
);
CREATE INDEX m_schedules_server ON m_schedules (node_id, server_id);

CREATE TABLE m_backups (
    node_id        uuid        NOT NULL,
    server_id      text        NOT NULL,
    backup_id      text        NOT NULL,
    kind           text        NOT NULL,
    status         text        NOT NULL,
    locked         boolean     NOT NULL,
    size           bigint      NOT NULL,
    files          bigint      NOT NULL,
    destination_id text        NOT NULL,
    error          text        NOT NULL,
    warning        text        NOT NULL,
    created_by     text        NOT NULL,
    created_at     timestamptz,
    finished_at    timestamptz,
    expires_at     timestamptz,
    PRIMARY KEY (node_id, backup_id),
    FOREIGN KEY (node_id, server_id) REFERENCES m_servers (node_id, server_id) ON DELETE CASCADE
);
CREATE INDEX m_backups_server ON m_backups (node_id, server_id);

-- +goose Down
DROP TABLE m_backups;
DROP TABLE m_schedules;
