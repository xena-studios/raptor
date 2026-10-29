-- +goose Up
-- Databases belong to their instance, and servers use them through links
-- (docs/DATABASES.md#the-model, DECISIONS #342): deleting a server removes
-- its links, never a database. Every database is linked to the server it
-- was made for. databases is rebuilt without server_id; nothing references
-- it yet, so the new table is filled, the links made pointing at it, the
-- old one dropped, and the new one renamed (which updates the links'
-- reference). Labels stay unique per server through its links: two
-- servers' databases on one instance can share one.
CREATE TABLE databases_new (
    id              TEXT PRIMARY KEY,
    instance_id     TEXT NOT NULL REFERENCES database_instances (id) ON DELETE CASCADE,
    label           TEXT NOT NULL,
    db_name         TEXT NOT NULL,
    username        TEXT NOT NULL,
    password        BLOB NOT NULL,
    created_at      INTEGER NOT NULL,
    ro_username     TEXT NOT NULL DEFAULT '',
    ro_password     BLOB,
    viewer_username TEXT NOT NULL DEFAULT '',
    viewer_password BLOB,
    UNIQUE (instance_id, db_name)
);
INSERT INTO databases_new (id, instance_id, label, db_name, username, password, created_at, ro_username, ro_password,
    viewer_username, viewer_password)
SELECT id, instance_id, label, db_name, username, password, created_at, ro_username, ro_password,
    viewer_username, viewer_password FROM databases;

CREATE TABLE database_links (
    database_id TEXT    NOT NULL REFERENCES databases_new (id) ON DELETE CASCADE,
    server_id   TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (database_id, server_id)
);
INSERT INTO database_links (database_id, server_id, created_at) SELECT id, server_id, created_at FROM databases;

DROP INDEX databases_server;
DROP TABLE databases;
ALTER TABLE databases_new RENAME TO databases;
CREATE INDEX database_links_server ON database_links (server_id);

-- +goose Down
-- An older Wings has one server per database: each keeps its first link,
-- and databases no server uses are forgotten (their data stays on the
-- instance).
CREATE TABLE databases_old (
    id              TEXT PRIMARY KEY,
    instance_id     TEXT NOT NULL REFERENCES database_instances (id) ON DELETE CASCADE,
    server_id       TEXT NOT NULL,
    label           TEXT NOT NULL,
    db_name         TEXT NOT NULL,
    username        TEXT NOT NULL,
    password        BLOB NOT NULL,
    created_at      INTEGER NOT NULL,
    ro_username     TEXT NOT NULL DEFAULT '',
    ro_password     BLOB,
    viewer_username TEXT NOT NULL DEFAULT '',
    viewer_password BLOB,
    UNIQUE (instance_id, db_name),
    UNIQUE (server_id, label)
);
INSERT OR IGNORE INTO databases_old (id, instance_id, server_id, label, db_name, username, password, created_at,
    ro_username, ro_password, viewer_username, viewer_password)
SELECT d.id, d.instance_id,
    (SELECT l.server_id FROM database_links l WHERE l.database_id = d.id ORDER BY l.created_at LIMIT 1),
    d.label, d.db_name, d.username, d.password, d.created_at, d.ro_username, d.ro_password,
    d.viewer_username, d.viewer_password
FROM databases d WHERE EXISTS (SELECT 1 FROM database_links l WHERE l.database_id = d.id);
DROP TABLE database_links;
DROP TABLE databases;
ALTER TABLE databases_old RENAME TO databases;
CREATE INDEX databases_server ON databases (server_id);
