-- +goose Up
-- Files deleted from the web file manager, kept a while to be restored
-- (docs/WINGS.md#trash). The files themselves are in
-- <disk>/.trash/<server_id>/<id>/<name>, outside the server's directory.
CREATE TABLE trash (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    path        TEXT NOT NULL, -- where it was, from the server's directory
    is_dir      INTEGER NOT NULL,
    size        INTEGER NOT NULL, -- bytes, everything in it for a directory
    deleted_at  INTEGER NOT NULL, -- unix ms
    deleted_by  TEXT NOT NULL
) STRICT;
CREATE INDEX trash_server ON trash (server_id, deleted_at);

-- +goose Down
DROP TABLE trash;
