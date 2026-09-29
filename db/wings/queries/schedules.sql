-- name: InsertSchedule :exec
INSERT INTO schedules (id, server_id, name, cron, timezone, enabled, only_when_online, jitter_s, missed, steps,
                       next_run_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSchedule :one
SELECT * FROM schedules WHERE id = ?;

-- name: ListSchedules :many
SELECT * FROM schedules WHERE (sqlc.arg(server_id) = '' OR server_id = sqlc.arg(server_id))
ORDER BY created_at, id;

-- name: CountServerSchedules :one
SELECT COUNT(*) FROM schedules WHERE server_id = ?;

-- name: UpdateSchedule :exec
UPDATE schedules
SET name = ?, cron = ?, timezone = ?, enabled = ?, only_when_online = ?, jitter_s = ?, missed = ?, steps = ?,
    next_run_at = ?, version = version + 1, updated_at = ?
WHERE id = ?;

-- name: DeleteSchedule :exec
DELETE FROM schedules WHERE id = ?;

-- name: DueSchedules :many
SELECT * FROM schedules WHERE next_run_at IS NOT NULL AND next_run_at <= ? ORDER BY next_run_at LIMIT 100;

-- name: NextScheduleRun :one
SELECT CAST(COALESCE(MIN(next_run_at), 0) AS INTEGER) FROM schedules WHERE next_run_at IS NOT NULL;

-- name: AdvanceSchedule :execrows
-- Moves a schedule past a run, unless it was changed meanwhile.
UPDATE schedules SET next_run_at = sqlc.arg(next_run_at), last_run_at = COALESCE(sqlc.narg(last_run_at), last_run_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version);

-- name: ActiveScheduleRuns :many
SELECT id FROM jobs
WHERE type = 'schedule.run' AND status IN ('queued', 'running') AND json_extract(payload, '$.schedule_id') = ?;
