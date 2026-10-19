-- +goose Up
-- Database instances Wings runs on the node, and the databases on them
-- for servers (docs/DATABASES.md).
CREATE TABLE database_instances (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    engine        TEXT NOT NULL CHECK (engine IN ('mariadb', 'mysql', 'postgres')),
    version       TEXT NOT NULL,                 -- a version line, like 11.4
    image         TEXT NOT NULL,                 -- the image it runs, with its digest
    root_password BLOB NOT NULL,                 -- sealed with the node's database key
    networks      TEXT NOT NULL DEFAULT '[]',    -- the owner's networks; [] is the server network
    limits        TEXT NOT NULL,                 -- JSON
    settings      TEXT NOT NULL,                 -- JSON
    desired_state TEXT NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running', 'stopped')),
    quota_project INTEGER,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Each instance's fixed address on each of the owner's networks it's on.
CREATE TABLE instance_addresses (
    instance_id TEXT NOT NULL REFERENCES database_instances (id) ON DELETE CASCADE,
    network_id  TEXT NOT NULL REFERENCES networks (id) ON DELETE CASCADE,
    ip          TEXT NOT NULL,
    PRIMARY KEY (instance_id, network_id),
    UNIQUE (network_id, ip)
);

CREATE TABLE databases (
    id          TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES database_instances (id) ON DELETE CASCADE,
    -- No foreign key: a deleted server's databases are dropped on their
    -- instance first (Service.ServerDeleted, and at start), then here.
    server_id   TEXT NOT NULL,
    label       TEXT NOT NULL,                   -- what the owner called it ("luckperms")
    db_name     TEXT NOT NULL,                   -- s<short id>_<label>
    username    TEXT NOT NULL,
    password    BLOB NOT NULL,                   -- sealed
    created_at  INTEGER NOT NULL,
    UNIQUE (instance_id, db_name),
    UNIQUE (server_id, label)
);
CREATE INDEX databases_server ON databases (server_id);

-- +goose Down
DROP TABLE databases;
DROP TABLE instance_addresses;
DROP TABLE database_instances;
