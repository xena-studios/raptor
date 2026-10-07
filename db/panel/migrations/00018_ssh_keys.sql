-- +goose Up
-- SFTP (docs/ARCHITECTURE.md#files-and-sftp): users log in with SSH keys as
-- <sftp_username>.<server short ID>. Key-only: accounts have no passwords.
ALTER TABLE users ADD COLUMN sftp_username text UNIQUE CHECK (sftp_username ~ '^[a-z0-9]{3,32}$');

CREATE TABLE ssh_keys (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    -- The key in SSH wire format, and its SHA256 fingerprint as ssh-keygen
    -- shows it.
    public_key   bytea       NOT NULL,
    fingerprint  text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (user_id, fingerprint)
);

-- +goose Down
DROP TABLE ssh_keys;
ALTER TABLE users DROP COLUMN sftp_username;
