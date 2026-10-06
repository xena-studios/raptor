-- +goose Up
-- TOTP two-factor authentication and recovery codes (docs/PANEL.md#auth).
-- The secret is encrypted with the Panel's data key (PANEL_DATA_KEY), bound
-- to the user's ID; totp_last_step stops a code from being used twice.
ALTER TABLE users
    ADD COLUMN totp_secret     bytea,
    ADD COLUMN totp_enabled_at timestamptz,
    ADD COLUMN totp_last_step  bigint NOT NULL DEFAULT 0;

-- A secret being set up: it only replaces users.totp_secret once the user
-- proves their app has it.
CREATE TABLE totp_setups (
    user_id    uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret     bytea       NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE TABLE recovery_codes (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  bytea       NOT NULL UNIQUE,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);

-- A sign-in waiting for its second factor. The browser holds the token in
-- a cookie, like a session.
CREATE TABLE pending_signins (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL UNIQUE,
    attempts   int         NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE pending_signins;
DROP TABLE recovery_codes;
DROP TABLE totp_setups;
ALTER TABLE users DROP COLUMN totp_last_step, DROP COLUMN totp_enabled_at, DROP COLUMN totp_secret;
