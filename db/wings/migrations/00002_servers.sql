-- +goose Up
CREATE TABLE servers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    -- A snapshot of the egg, taken at create/reinstall time (docs/SERVERS.md).
    egg           BLOB NOT NULL,
    egg_source    TEXT NOT NULL DEFAULT '',
    egg_hash      TEXT NOT NULL,
    image         TEXT NOT NULL,
    startup       TEXT NOT NULL,
    variables     TEXT NOT NULL DEFAULT '{}', -- JSON object
    limits        TEXT NOT NULL DEFAULT '{}', -- JSON
    settings      TEXT NOT NULL DEFAULT '{}', -- JSON
    host_network  INTEGER NOT NULL DEFAULT 0 CHECK (host_network IN (0, 1)),
    desired_state TEXT NOT NULL DEFAULT 'stopped' CHECK (desired_state IN ('running', 'stopped')),
    install_state TEXT NOT NULL DEFAULT 'pending' CHECK (install_state IN ('pending', 'installing', 'installed', 'failed')),
    install_error TEXT NOT NULL DEFAULT '',
    -- The last runtime state Wings saw, so a server that was already running
    -- is adopted as running after a Wings restart (its "done" line may have
    -- scrolled out of the console history).
    last_state    TEXT NOT NULL DEFAULT '',
    version       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE allocations (
    server_id  TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    ip         TEXT NOT NULL,
    port       INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    PRIMARY KEY (ip, port)
) STRICT;

CREATE UNIQUE INDEX allocations_one_primary ON allocations (server_id) WHERE is_primary = 1;
CREATE INDEX allocations_port ON allocations (port);

-- +goose Down
DROP TABLE allocations;
DROP TABLE servers;
