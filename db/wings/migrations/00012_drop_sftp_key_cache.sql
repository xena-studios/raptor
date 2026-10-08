-- +goose Up
-- SFTP takes only temporary passwords, which the Panel checks every time and
-- are never cached (docs/DECISIONS.md #223): the key cache is gone.
DROP TABLE sftp_key_cache;

-- +goose Down
CREATE TABLE sftp_key_cache (
    username     TEXT NOT NULL,
    server_id    TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    fingerprint  TEXT NOT NULL,
    public_key   BLOB NOT NULL,
    user_id      TEXT NOT NULL,
    permissions  TEXT NOT NULL DEFAULT '[]',
    confirmed_at INTEGER NOT NULL,
    PRIMARY KEY (username, server_id, fingerprint)
) STRICT;
CREATE INDEX sftp_key_cache_server ON sftp_key_cache (server_id);
