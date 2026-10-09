-- name: UpsertMirrorServer :exec
-- A fetch is the node's current answer; an older one (a slower fetch that
-- lost a race) doesn't replace a newer version.
INSERT INTO m_servers (node_id, server_id, name, version, state, desired_state, install_state,
                       install_error, egg_name, egg_source, config, created_at, updated_at, synced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
ON CONFLICT (node_id, server_id) DO UPDATE SET
    name = EXCLUDED.name, version = EXCLUDED.version, state = EXCLUDED.state,
    desired_state = EXCLUDED.desired_state, install_state = EXCLUDED.install_state,
    install_error = EXCLUDED.install_error, egg_name = EXCLUDED.egg_name,
    egg_source = EXCLUDED.egg_source, config = EXCLUDED.config,
    created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at, synced_at = now()
WHERE m_servers.version <= EXCLUDED.version;

-- name: SetMirrorServerState :exec
UPDATE m_servers SET state = $3, synced_at = now() WHERE node_id = $1 AND server_id = $2;

-- name: DeleteMirrorServer :exec
DELETE FROM m_servers WHERE node_id = $1 AND server_id = $2;

-- name: DeleteMirrorServers :exec
DELETE FROM m_servers WHERE node_id = $1;

-- name: ListMirrorServers :many
SELECT * FROM m_servers WHERE node_id = $1 ORDER BY name, server_id;

-- name: GetNodeAcked :one
SELECT last_acked_seq FROM nodes WHERE id = $1;

-- name: SetNodeAcked :exec
UPDATE nodes SET last_acked_seq = $2 WHERE id = $1;

-- name: DeleteMirrorSchedules :exec
DELETE FROM m_schedules WHERE node_id = $1 AND server_id = $2;

-- name: InsertMirrorSchedule :exec
INSERT INTO m_schedules (node_id, server_id, schedule_id, name, enabled, version, next_run, last_run, definition)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListMirrorSchedules :many
-- last_run counts Run now too, which the node's doesn't.
SELECT s.node_id, s.server_id, s.schedule_id, s.name, s.enabled, s.version, s.next_run,
       GREATEST(s.last_run, (SELECT max(r.started_at) FROM schedule_runs r
                             WHERE r.node_id = s.node_id AND r.schedule_id = s.schedule_id
                               AND r.status <> 'skipped'))::timestamptz AS last_run,
       s.definition
FROM m_schedules s WHERE s.node_id = $1 AND s.server_id = $2 ORDER BY s.name, s.schedule_id;

-- name: DeleteMirrorBackups :exec
DELETE FROM m_backups WHERE node_id = $1 AND server_id = $2;

-- name: InsertMirrorBackup :exec
INSERT INTO m_backups (node_id, server_id, backup_id, kind, status, locked, size, files, destination_id,
                       error, warning, created_by, created_at, finished_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: ListMirrorBackups :many
SELECT * FROM m_backups WHERE node_id = $1 AND server_id = $2 ORDER BY created_at DESC, backup_id;

-- name: DeleteMirrorJobs :exec
DELETE FROM m_jobs WHERE node_id = $1 AND server_id = $2;

-- name: InsertMirrorJob :exec
INSERT INTO m_jobs (node_id, server_id, job_id, type, status, attempts, error, created_at, started_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListMirrorJobs :many
SELECT * FROM m_jobs WHERE node_id = $1 AND server_id = $2 ORDER BY created_at DESC, job_id;

-- name: StartScheduleRun :exec
INSERT INTO schedule_runs (node_id, server_id, schedule_id, run_key, status, reason, skip_reason, scheduled_for, started_at, finished_at)
VALUES (@node_id, @server_id, @schedule_id, @run_key, @status, @reason, @skip_reason, @scheduled_for, @started_at, @finished_at)
ON CONFLICT (node_id, run_key) DO NOTHING;

-- name: FinishScheduleRun :exec
-- The queued event may be gone (pruned before the Panel saw it): the run
-- is recorded from its end alone.
INSERT INTO schedule_runs (node_id, server_id, schedule_id, run_key, status, reason, steps, error, started_at, finished_at)
VALUES (@node_id, @server_id, @schedule_id, @run_key, @status, @reason, @steps, @error, @finished_at, @finished_at)
ON CONFLICT (node_id, run_key) DO UPDATE
SET status = excluded.status, steps = excluded.steps, error = excluded.error, finished_at = excluded.finished_at;

-- name: TrimScheduleRuns :exec
-- Keeps a schedule's newest runs.
DELETE FROM schedule_runs r
WHERE r.node_id = @node_id AND r.schedule_id = @schedule_id
  AND r.id <= (SELECT s.id FROM schedule_runs s
            WHERE s.node_id = @node_id AND s.schedule_id = @schedule_id
            ORDER BY id DESC OFFSET @keep::int LIMIT 1);

-- name: DeleteScheduleRuns :exec
DELETE FROM schedule_runs WHERE node_id = @node_id AND schedule_id = @schedule_id;

-- name: DeleteServerScheduleRuns :exec
DELETE FROM schedule_runs WHERE node_id = @node_id AND server_id = @server_id;

-- name: ListScheduleRuns :many
-- A server's runs, newest first; one schedule's if schedule_id isn't empty.
SELECT * FROM schedule_runs
WHERE node_id = @node_id AND server_id = @server_id
  AND (@schedule_id::text = '' OR schedule_id = @schedule_id)
  AND (@before::bigint = 0 OR id < @before)
ORDER BY id DESC
LIMIT @lim;
