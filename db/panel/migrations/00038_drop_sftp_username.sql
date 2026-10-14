-- +goose Up
-- Held back from v0.3.0 (00035): v0.2.0's queries still named it. No
-- release reads it now (docs/DECISIONS.md #253).
ALTER TABLE users DROP COLUMN sftp_username;

-- +goose Down
ALTER TABLE users ADD COLUMN sftp_username text UNIQUE CHECK (sftp_username ~ '^[a-z0-9]{3,32}$');
