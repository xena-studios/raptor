-- +goose Up
-- Version changes and backups of databases (docs/DATABASES.md#changing-an-instances-version):
-- the version an instance ran before its last change, kept to go back to
-- until rollback_until, and the day its patch update was last tried.
ALTER TABLE database_instances ADD COLUMN prev_version TEXT NOT NULL DEFAULT '';
ALTER TABLE database_instances ADD COLUMN prev_image TEXT NOT NULL DEFAULT '';
ALTER TABLE database_instances ADD COLUMN rollback_until INTEGER NOT NULL DEFAULT 0; -- unix seconds
ALTER TABLE database_instances ADD COLUMN patch_tried TEXT NOT NULL DEFAULT '';     -- YYYY-MM-DD, the node's time

-- The databases a backup holds a dump of, by their names on the server.
ALTER TABLE backups ADD COLUMN databases TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE backups DROP COLUMN databases;
ALTER TABLE database_instances DROP COLUMN patch_tried;
ALTER TABLE database_instances DROP COLUMN rollback_until;
ALTER TABLE database_instances DROP COLUMN prev_image;
ALTER TABLE database_instances DROP COLUMN prev_version;
