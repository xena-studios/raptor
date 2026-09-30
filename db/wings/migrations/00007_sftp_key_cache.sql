-- +goose Up
-- SSH public keys the Panel accepted for SFTP logins, with the permissions it
-- granted (docs/WINGS.md#files-and-sftp). Key logins fall back to this cache
-- while the Panel is unreachable. Public keys aren't secrets; passwords are
-- never cached (docs/DECISIONS.md #29).
CREATE TABLE sftp_key_cache (
    username     TEXT NOT NULL,
    server_id    TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    fingerprint  TEXT NOT NULL, -- SHA256:… of the public key
    public_key   BLOB NOT NULL, -- SSH wire format
    user_id      TEXT NOT NULL,
    permissions  TEXT NOT NULL DEFAULT '[]', -- JSON list
    confirmed_at INTEGER NOT NULL, -- unix ms, when the Panel last accepted it
    PRIMARY KEY (username, server_id, fingerprint)
) STRICT;

CREATE INDEX sftp_key_cache_server ON sftp_key_cache (server_id);

-- +goose Down
DROP TABLE sftp_key_cache;
