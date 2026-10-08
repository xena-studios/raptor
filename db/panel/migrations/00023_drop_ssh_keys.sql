-- +goose Up
-- SFTP takes only temporary passwords (docs/DECISIONS.md #223): SSH keys and
-- the per-account SFTP username they logged in with are gone.
DROP TABLE ssh_keys;
ALTER TABLE users DROP COLUMN sftp_username;

-- +goose Down
ALTER TABLE users ADD COLUMN sftp_username text UNIQUE CHECK (sftp_username ~ '^[a-z0-9]{3,32}$');
CREATE TABLE ssh_keys (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    public_key   bytea       NOT NULL,
    fingerprint  text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (user_id, fingerprint)
);
