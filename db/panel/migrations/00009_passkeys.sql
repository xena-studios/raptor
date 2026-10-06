-- +goose Up
-- Passkeys (docs/PANEL.md#auth). The user handle passkeys store is random,
-- not the user's ID, so it says nothing about the account.
ALTER TABLE users ADD COLUMN webauthn_handle bytea UNIQUE;

-- One row per passkey. credential is go-webauthn's record (public key,
-- counter, flags, transports); credential_id is pulled out to look it up.
CREATE TABLE passkeys (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id bytea       NOT NULL UNIQUE,
    credential    jsonb       NOT NULL,
    name          text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);
CREATE INDEX passkeys_user ON passkeys (user_id);

-- A WebAuthn ceremony in progress: the challenge the browser must sign,
-- used once. Sign-in ceremonies have no user yet.
CREATE TABLE webauthn_ceremonies (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    purpose    text        NOT NULL CHECK (purpose IN ('register', 'signin', 'reauth')),
    session_id uuid        REFERENCES sessions (id) ON DELETE CASCADE,
    data       jsonb       NOT NULL,
    expires_at timestamptz NOT NULL
);

-- Sign-in codes and re-authentication codes are different things.
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose CHECK (purpose IN ('signin', 'reauth'));

-- +goose Down
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose;
DROP TABLE webauthn_ceremonies;
DROP TABLE passkeys;
ALTER TABLE users DROP COLUMN webauthn_handle;
