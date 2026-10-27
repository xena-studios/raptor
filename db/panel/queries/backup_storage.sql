-- name: OrgBackupStorage :many
SELECT * FROM backup_storage WHERE org_id = $1 AND disabled_at IS NULL;

-- name: InsertBackupStorage :one
INSERT INTO backup_storage (org_id, node_id, key_id, enabled_by) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: DisableBackupStorage :one
UPDATE backup_storage SET disabled_at = @disabled_at WHERE node_id = @node_id AND disabled_at IS NULL RETURNING *;

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

-- name: CancelBackupStoragePurge :exec
-- Turned on again: the folder is in use, so nothing turned off before is
-- deleted (or reminded about).
UPDATE backup_storage SET purged_at = now()
WHERE node_id = $1 AND disabled_at IS NOT NULL AND purged_at IS NULL;

-- name: BackupStorageToRemind :many
-- Turned off, data not deleted yet: for the reminders before it is.
SELECT s.id, s.org_id, s.node_id, s.disabled_at, s.notified,
       coalesce(n.name, '')::text AS node_name, (n.deleted_at IS NOT NULL)::bool AS node_removed
FROM backup_storage s LEFT JOIN nodes n ON n.id = s.node_id
WHERE s.disabled_at IS NOT NULL AND s.purged_at IS NULL;

-- name: SetBackupStorageNotified :exec
UPDATE backup_storage SET notified = $2 WHERE id = $1;
