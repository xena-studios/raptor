-- +goose Up
-- Each server's XFS quota project (docs/WINGS.md#disk-quotas). NULL for
-- servers created before quotas; Wings assigns one before their next start.
ALTER TABLE servers ADD COLUMN quota_project INTEGER;
CREATE UNIQUE INDEX servers_quota_project ON servers (quota_project) WHERE quota_project IS NOT NULL;

-- +goose Down
DROP INDEX servers_quota_project;
ALTER TABLE servers DROP COLUMN quota_project;
