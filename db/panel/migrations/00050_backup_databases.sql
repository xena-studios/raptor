-- +goose Up
-- The server's databases a backup holds a dump of (docs/DATABASES.md#backups).
ALTER TABLE m_backups ADD COLUMN databases text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE m_backups DROP COLUMN databases;
