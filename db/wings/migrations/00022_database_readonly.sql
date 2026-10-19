-- +goose Up
-- A database's read-only user, for tools that should only look
-- (docs/DATABASES.md#the-browser): made on request.
ALTER TABLE databases ADD COLUMN ro_username TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN ro_password BLOB;

-- +goose Down
ALTER TABLE databases DROP COLUMN ro_password;
ALTER TABLE databases DROP COLUMN ro_username;
