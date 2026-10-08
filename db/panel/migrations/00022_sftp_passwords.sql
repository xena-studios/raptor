-- +goose Up
-- Temporary SFTP passwords (docs/DECISIONS.md #222): a user turns SFTP on
-- for one server and gets a generated username and password that work
-- until they turn it off or it runs out (a day unless they pick otherwise).
-- The password is random and long, so a SHA-256 of it is enough; only the
-- hash is stored.
CREATE TABLE sftp_passwords (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id     uuid        NOT NULL REFERENCES nodes (id),
    server_id   text        NOT NULL,
    -- The login's first part (<username>.<server short ID>). The hyphen
    -- keeps it apart from users.sftp_username, which key logins use.
    username    text        NOT NULL UNIQUE CHECK (username ~ '^t-[a-z0-9]{10}$'),
    secret_hash bytea       NOT NULL,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, node_id, server_id)
);

-- Whether a node serves SFTP, on which port, and its host key, from Wings'
-- node.sftp events.
ALTER TABLE nodes ADD COLUMN sftp_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE nodes ADD COLUMN sftp_port integer NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN sftp_host_key text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE nodes DROP COLUMN sftp_host_key;
ALTER TABLE nodes DROP COLUMN sftp_port;
ALTER TABLE nodes DROP COLUMN sftp_enabled;
DROP TABLE sftp_passwords;
