-- name: ActiveBackupStorage :one
SELECT * FROM backup_storage WHERE node_id = $1 AND disabled_at IS NULL;

-- name: OrgBackupStorage :many
SELECT * FROM backup_storage WHERE org_id = $1 AND disabled_at IS NULL;

-- name: InsertBackupStorage :one
INSERT INTO backup_storage (org_id, node_id, key_id, enabled_by) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: DisableBackupStorage :one
UPDATE backup_storage SET disabled_at = now() WHERE node_id = $1 AND disabled_at IS NULL RETURNING *;

-- name: BackupStorageToPurge :many
-- Turned off over 30 days ago, data not deleted yet.
SELECT * FROM backup_storage WHERE purged_at IS NULL AND disabled_at < @before;

-- name: SetBackupStoragePurged :exec
UPDATE backup_storage SET purged_at = now() WHERE id = $1;

-- name: BackupStorageOrgsToMeasure :many
-- Orgs with storage on, or data not deleted yet, not measured on day.
SELECT DISTINCT s.org_id FROM backup_storage s
WHERE s.purged_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM backup_storage_usage u WHERE u.org_id = s.org_id AND u.day = @day);

-- name: InsertBackupStorageUsage :exec
INSERT INTO backup_storage_usage (org_id, day, bytes) VALUES ($1, $2, $3)
ON CONFLICT (org_id, day) DO UPDATE SET bytes = excluded.bytes, measured_at = now();

-- name: LatestBackupStorageUsage :one
SELECT * FROM backup_storage_usage WHERE org_id = $1 ORDER BY day DESC LIMIT 1;
