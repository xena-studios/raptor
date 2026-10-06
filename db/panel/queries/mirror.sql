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
SELECT * FROM m_schedules WHERE node_id = $1 AND server_id = $2 ORDER BY name, schedule_id;

-- name: DeleteMirrorBackups :exec
DELETE FROM m_backups WHERE node_id = $1 AND server_id = $2;

-- name: InsertMirrorBackup :exec
INSERT INTO m_backups (node_id, server_id, backup_id, kind, status, locked, size, files, destination_id,
                       error, warning, created_by, created_at, finished_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: ListMirrorBackups :many
SELECT * FROM m_backups WHERE node_id = $1 AND server_id = $2 ORDER BY created_at DESC, backup_id;
