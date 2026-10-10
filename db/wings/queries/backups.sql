-- name: InsertBackupDestination :exec
INSERT INTO backup_destinations (id, name, type, config, upload_limit, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetBackupDestination :one
SELECT * FROM backup_destinations WHERE id = ?;

-- name: ListBackupDestinations :many
SELECT * FROM backup_destinations ORDER BY id != 'local', created_at, id; -- local first

-- name: UpdateBackupDestination :exec
UPDATE backup_destinations SET name = ?, config = ?, upload_limit = ?, version = version + 1, updated_at = ? WHERE id = ?;

-- name: DestinationWorked :exec
UPDATE backup_destinations SET last_ok_at = ? WHERE id = ?;

-- name: DestinationFailed :exec
UPDATE backup_destinations SET last_error = ?, last_error_at = ? WHERE id = ?;

-- name: SetDestinationSize :exec
UPDATE backup_destinations SET size = ?, size_at = ? WHERE id = ?;

-- name: DeleteBackupDestination :exec
DELETE FROM backup_destinations WHERE id = ?;

-- name: CountDestinationTargets :one
SELECT COUNT(*) FROM backup_targets WHERE destination_id = ?;

-- name: ListBackupTargets :many
SELECT * FROM backup_targets WHERE server_id = ? ORDER BY position;

-- name: DeleteBackupTargets :exec
DELETE FROM backup_targets WHERE server_id = ?;

-- name: InsertBackupTarget :exec
INSERT INTO backup_targets (server_id, destination_id, position, keep_last, keep_daily, keep_weekly, keep_monthly)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetBackupPolicy :one
SELECT * FROM backup_policies WHERE server_id = ?;

-- name: UpsertBackupPolicy :exec
INSERT INTO backup_policies (server_id, destination_id, keep_last, keep_daily, keep_weekly, keep_monthly, ignore, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id) DO UPDATE
SET destination_id = excluded.destination_id, keep_last = excluded.keep_last, keep_daily = excluded.keep_daily,
    keep_weekly = excluded.keep_weekly, keep_monthly = excluded.keep_monthly, ignore = excluded.ignore,
    version = backup_policies.version + 1, updated_at = excluded.updated_at;

-- name: InsertBackup :exec
INSERT INTO backups (id, server_id, destination_id, kind, locked, job_id, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetBackup :one
SELECT * FROM backups WHERE id = ?;

-- name: ListBackups :many
SELECT * FROM backups WHERE (sqlc.arg(server_id) = '' OR server_id = sqlc.arg(server_id))
ORDER BY created_at DESC, id DESC;

-- name: ListDestinationBackups :many
SELECT * FROM backups WHERE destination_id = ? AND server_id = ?;

-- name: SetBackupRunning :exec
UPDATE backups SET status = 'running', error = '' WHERE id = ?;

-- name: FinishBackup :exec
UPDATE backups
SET status = 'ok', snapshot_id = ?, size = ?, files = ?, uploaded = ?, warning = ?, error = '', finished_at = ?
WHERE id = ?;

-- name: FailBackup :exec
UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?;

-- name: SetBackupLocked :exec
UPDATE backups SET locked = ? WHERE id = ?;

-- name: DeleteBackupRow :exec
DELETE FROM backups WHERE id = ?;

-- name: ExpiredBackups :many
-- Safety backups past their expiry, and failed backups older than the cutoff.
SELECT * FROM backups
WHERE locked = 0 AND ((expires_at IS NOT NULL AND expires_at <= sqlc.arg(now))
    OR (status = 'failed' AND created_at <= sqlc.arg(failed_before)));

-- name: GetJobBackup :one
-- The backup a job takes for itself (a safety or final backup), so a
-- resumed job finds it again.
SELECT * FROM backups WHERE job_id = ? AND kind = ? ORDER BY created_at DESC LIMIT 1;
