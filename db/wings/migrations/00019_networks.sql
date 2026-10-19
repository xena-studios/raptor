-- +goose Up
-- Networks the owner made (docs/NETWORKS.md): Docker bridges Wings owns,
-- besides the default server network (raptor_nw). A server's networks are
-- a JSON list of their IDs, its primary first; empty is the default one.
CREATE TABLE networks (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    subnet      TEXT    NOT NULL,
    internal    INTEGER NOT NULL DEFAULT 0 CHECK (internal IN (0, 1)),
    ipv6        INTEGER NOT NULL DEFAULT 0 CHECK (ipv6 IN (0, 1)),
    mtu         INTEGER NOT NULL DEFAULT 0,
    host_access INTEGER NOT NULL DEFAULT 1 CHECK (host_access IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

ALTER TABLE servers ADD COLUMN networks TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE servers DROP COLUMN networks;
DROP TABLE networks;
