-- +goose Up
-- Several `panel serve api` instances (docs/ARCHITECTURE.md#panel): each
-- holds some nodes' connections, and a request for a node another instance
-- holds is forwarded to it through Postgres.

-- Instances say they're alive every few seconds; one that stops is ignored.
CREATE TABLE panel_instances (
    id         text        PRIMARY KEY,
    started_at timestamptz NOT NULL DEFAULT now(),
    seen_at    timestamptz NOT NULL DEFAULT now()
);

-- Which instance holds each connected node.
CREATE TABLE node_connections (
    node_id      uuid        PRIMARY KEY REFERENCES nodes (id),
    instance_id  text        NOT NULL,
    connected_at timestamptz NOT NULL DEFAULT now()
);

-- Forwarded requests and their answers. Short-lived and rebuilt by retries,
-- so not worth the WAL.
CREATE UNLOGGED TABLE node_requests (
    id         bigserial   PRIMARY KEY,
    node_id    uuid        NOT NULL,
    origin     text        NOT NULL,
    target     text        NOT NULL,
    method     text        NOT NULL,
    request    bytea       NOT NULL,
    response   bytea,
    error_code text        NOT NULL DEFAULT '',
    error      text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    done_at    timestamptz
);
CREATE INDEX node_requests_created ON node_requests (created_at);
CREATE INDEX node_requests_pending ON node_requests (target) WHERE claimed_at IS NULL;

-- +goose Down
DROP TABLE node_requests;
DROP TABLE node_connections;
DROP TABLE panel_instances;
