-- +goose Up
-- Raptor's phone app (docs/PANEL.md#the-phone-app). It signs in through the
-- web app and holds bearer tokens instead of a cookie: a short-lived access
-- token (token_hash, until access_expires_at) and a refresh token that's
-- replaced every time it's used. A browser session has client ''.
ALTER TABLE sessions
    ADD COLUMN client                text NOT NULL DEFAULT '',
    ADD COLUMN device_name           text NOT NULL DEFAULT '',
    ADD COLUMN access_expires_at     timestamptz,
    ADD COLUMN refresh_hash          bytea UNIQUE,
    ADD COLUMN previous_refresh_hash bytea;
CREATE INDEX sessions_previous_refresh ON sessions (previous_refresh_hash) WHERE previous_refresh_hash IS NOT NULL;

-- A signed-in browser's approval for the app, traded once (with the PKCE
-- verifier) for a session at POST /oauth/token.
CREATE TABLE app_codes (
    code_hash    bytea       PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client       text        NOT NULL,
    redirect_uri text        NOT NULL,
    challenge    text        NOT NULL,
    device_name  text        NOT NULL,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app_codes;
DROP INDEX sessions_previous_refresh;
ALTER TABLE sessions
    DROP COLUMN previous_refresh_hash,
    DROP COLUMN refresh_hash,
    DROP COLUMN access_expires_at,
    DROP COLUMN device_name,
    DROP COLUMN client;
