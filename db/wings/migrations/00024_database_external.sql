-- +goose Up
-- External access to an instance (docs/DATABASES.md#external-access): its
-- port on the node (0 is off) and the addresses allowed to reach it.
ALTER TABLE database_instances ADD COLUMN external_port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE database_instances ADD COLUMN external_allow TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE database_instances DROP COLUMN external_allow;
ALTER TABLE database_instances DROP COLUMN external_port;
