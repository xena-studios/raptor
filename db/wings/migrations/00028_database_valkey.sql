-- +goose Up
-- Valkey instances, beside Redis (docs/DATABASES.md#engines-and-versions,
-- DECISIONS #333). As in 00025:
-- SQLite can't change a CHECK constraint, so database_instances is rebuilt.
-- Dropping it would cascade into its databases and addresses, so they're
-- rebuilt with it: the new tables are filled, the old children dropped
-- before their parent, then everything renamed (renaming the parent
-- updates the children's references).
CREATE TABLE database_instances_new (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    engine         TEXT NOT NULL CHECK (engine IN ('mariadb', 'mysql', 'postgres', 'redis', 'mongodb', 'valkey')),
    version        TEXT NOT NULL,
    image          TEXT NOT NULL,
    root_password  BLOB NOT NULL,
    networks       TEXT NOT NULL DEFAULT '[]',
    limits         TEXT NOT NULL,
    settings       TEXT NOT NULL,
    desired_state  TEXT NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running', 'stopped')),
    quota_project  INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    prev_version   TEXT NOT NULL DEFAULT '',
    prev_image     TEXT NOT NULL DEFAULT '',
    rollback_until INTEGER NOT NULL DEFAULT 0,
    patch_tried    TEXT NOT NULL DEFAULT '',
    external_port  INTEGER NOT NULL DEFAULT 0,
    external_allow TEXT NOT NULL DEFAULT '[]'
);
INSERT INTO database_instances_new (id, name, engine, version, image, root_password, networks, limits, settings,
    desired_state, quota_project, created_at, updated_at, prev_version, prev_image, rollback_until, patch_tried,
    external_port, external_allow)
SELECT id, name, engine, version, image, root_password, networks, limits, settings,
    desired_state, quota_project, created_at, updated_at, prev_version, prev_image, rollback_until, patch_tried,
    external_port, external_allow
FROM database_instances;

CREATE TABLE instance_addresses_new (
    instance_id TEXT NOT NULL REFERENCES database_instances_new (id) ON DELETE CASCADE,
    network_id  TEXT NOT NULL REFERENCES networks (id) ON DELETE CASCADE,
    ip          TEXT NOT NULL,
    PRIMARY KEY (instance_id, network_id),
    UNIQUE (network_id, ip)
);
INSERT INTO instance_addresses_new (instance_id, network_id, ip) SELECT instance_id, network_id, ip FROM instance_addresses;

CREATE TABLE databases_new (
    id          TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES database_instances_new (id) ON DELETE CASCADE,
    server_id   TEXT NOT NULL,
    label       TEXT NOT NULL,
    db_name     TEXT NOT NULL,
    username    TEXT NOT NULL,
    password    BLOB NOT NULL,
    created_at  INTEGER NOT NULL,
    ro_username TEXT NOT NULL DEFAULT '',
    ro_password BLOB,
    viewer_username TEXT NOT NULL DEFAULT '',
    viewer_password BLOB,
    UNIQUE (instance_id, db_name),
    UNIQUE (server_id, label)
);
INSERT INTO databases_new (id, instance_id, server_id, label, db_name, username, password, created_at, ro_username, ro_password,
    viewer_username, viewer_password)
SELECT id, instance_id, server_id, label, db_name, username, password, created_at, ro_username, ro_password,
    viewer_username, viewer_password FROM databases;

DROP TABLE databases;
DROP TABLE instance_addresses;
DROP TABLE database_instances;
ALTER TABLE database_instances_new RENAME TO database_instances;
ALTER TABLE instance_addresses_new RENAME TO instance_addresses;
ALTER TABLE databases_new RENAME TO databases;
CREATE INDEX databases_server ON databases (server_id);

-- +goose Down
-- Valkey instances can't be kept by an older Wings.
DELETE FROM database_instances WHERE engine = 'valkey';
