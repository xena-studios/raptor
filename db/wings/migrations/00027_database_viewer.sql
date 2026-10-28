-- +goose Up
-- The browser's reader for a database (docs/DATABASES.md#permissions): a
-- read-only user Wings makes when someone who may only look first runs
-- SQL, reachable only through the instance's socket, and never shown.
ALTER TABLE databases ADD COLUMN viewer_username TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN viewer_password BLOB;

-- +goose Down
ALTER TABLE databases DROP COLUMN viewer_password;
ALTER TABLE databases DROP COLUMN viewer_username;
