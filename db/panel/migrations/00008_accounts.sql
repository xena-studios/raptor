-- +goose Up
-- Accounts (docs/PANEL.md#auth): passwordless. Sign-in methods, sessions,
-- and the email codes that sign people up and in.
CREATE TABLE users (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    email             text        NOT NULL UNIQUE CHECK (email = lower(email)),
    email_verified_at timestamptz,
    name              text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- Sessions: the cookie holds a random token; only its hash is stored.
CREATE TABLE sessions (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    reauth_at    timestamptz,
    ip           inet,
    user_agent   text        NOT NULL DEFAULT '',
    revoked_at   timestamptz
);
CREATE INDEX sessions_user ON sessions (user_id) WHERE revoked_at IS NULL;

-- One email carries a 6-digit code and a link; either signs in, once.
CREATE TABLE email_codes (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    email           text        NOT NULL,
    code_hash       bytea       NOT NULL,
    link_token_hash bytea       NOT NULL UNIQUE,
    purpose         text        NOT NULL DEFAULT 'signin',
    attempts        int         NOT NULL DEFAULT 0,
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_codes_email ON email_codes (email, created_at DESC);

-- Rate limits shared by every Panel instance: one row per counted event.
CREATE UNLOGGED TABLE rate_events (
    key text        NOT NULL,
    at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rate_events_key ON rate_events (key, at);

-- +goose Down
DROP TABLE rate_events;
DROP TABLE email_codes;
DROP TABLE sessions;
DROP TABLE users;
