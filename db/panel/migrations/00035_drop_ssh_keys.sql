-- +goose Up
-- SSH keys are gone (docs/DECISIONS.md #223), and no release reads this
-- table any more. users.sftp_username is dropped in the release after
-- v0.3.0: v0.2.0's queries still name it, and migrations run while the old
-- version is serving (docs/DEPLOY.md). Until then the generated code
-- doesn't see it (db/panel/pending).
DROP TABLE ssh_keys;

-- +goose Down
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
