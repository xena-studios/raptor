-- +goose Up
-- The mirror (docs/ARCHITECTURE.md#mirror-sync): servers as their nodes
-- report them. Disposable: it can be dropped and rebuilt from the nodes, and
-- the node is always right.
CREATE TABLE m_servers (
    node_id       uuid        NOT NULL REFERENCES nodes (id),
    server_id     text        NOT NULL,
    name          text        NOT NULL,
    version       bigint      NOT NULL,
    state         text        NOT NULL DEFAULT '',
    desired_state text        NOT NULL DEFAULT '',
    install_state text        NOT NULL DEFAULT '',
    install_error text        NOT NULL DEFAULT '',
    egg_name      text        NOT NULL DEFAULT '',
    egg_source    text        NOT NULL DEFAULT '',
    config        jsonb       NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    synced_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, server_id)
);

-- nodes.last_acked_seq is -1 while a node has no mirror (it gets a
-- snapshot), so 0 can mean "in sync with a node that has no events yet".
ALTER TABLE nodes ALTER COLUMN last_acked_seq SET DEFAULT -1;
UPDATE nodes SET last_acked_seq = -1 WHERE last_acked_seq = 0;

-- +goose Down
ALTER TABLE nodes ALTER COLUMN last_acked_seq SET DEFAULT 0;
DROP TABLE m_servers;
