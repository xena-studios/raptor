-- +goose Up
-- Sign-in with Google, GitHub, and Discord (docs/PANEL.md#auth).
CREATE TABLE oauth_accounts (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider       text        NOT NULL CHECK (provider IN ('google', 'github', 'discord')),
    subject        text        NOT NULL,
    email          text        NOT NULL DEFAULT '',
    email_verified bool        NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_used_at   timestamptz,
    UNIQUE (provider, subject)
);
CREATE INDEX oauth_accounts_user ON oauth_accounts (user_id);

-- A trip to a provider and back. The browser holds the state in a cookie;
-- linking flows remember the session that started them.
CREATE TABLE oauth_flows (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    state_hash bytea       NOT NULL UNIQUE,
    provider   text        NOT NULL,
    verifier   text        NOT NULL,
    nonce      text        NOT NULL,
    session_id uuid        REFERENCES sessions (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE oauth_flows;
DROP TABLE oauth_accounts;
