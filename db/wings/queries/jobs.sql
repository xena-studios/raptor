-- name: InsertJob :exec
INSERT INTO jobs (id, server_id, type, payload, status, max_attempts, run_after, created_at)
VALUES (?, ?, ?, ?, 'queued', ?, ?, ?);

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: ListJobs :many
SELECT * FROM jobs WHERE (sqlc.arg(server_id) = '' OR server_id = sqlc.arg(server_id))
ORDER BY created_at DESC LIMIT sqlc.arg(lim);

-- name: RunnableJobs :many
SELECT * FROM jobs WHERE status = 'queued' AND run_after <= ? ORDER BY run_after, created_at LIMIT 100;

-- name: NextRunAfter :one
SELECT CAST(COALESCE(MIN(run_after), 0) AS INTEGER) FROM jobs WHERE status = 'queued';

-- name: ActiveJobsForServer :one
SELECT COUNT(*) FROM jobs WHERE server_id = ? AND status IN ('queued', 'running');

-- name: RunningJobs :many
SELECT * FROM jobs WHERE status = 'running';

-- name: MarkJobRunning :execrows
UPDATE jobs SET status = 'running', attempts = attempts + 1, started_at = ?
WHERE id = ? AND status = 'queued';

-- name: FinishJob :exec
UPDATE jobs SET status = ?, error = ?, result = ?, finished_at = ? WHERE id = ?;

-- name: RequeueJob :exec
UPDATE jobs SET status = 'queued', error = ?, run_after = ? WHERE id = ?;

-- name: CancelQueuedJob :execrows
UPDATE jobs SET status = 'cancelled', error = 'cancelled', finished_at = ? WHERE id = ? AND status = 'queued';

-- name: PruneJobs :many
DELETE FROM jobs WHERE status IN ('succeeded', 'failed', 'cancelled') AND finished_at < ? RETURNING id;
