-- name: ListInstances :many
SELECT * FROM database_instances ORDER BY created_at, id;

-- name: GetInstance :one
SELECT * FROM database_instances WHERE id = ?;

-- name: InsertInstance :exec
INSERT INTO database_instances (id, name, engine, version, image, root_password, networks, limits, settings,
                                desired_state, quota_project, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateInstance :exec
UPDATE database_instances
SET name = ?, networks = ?, limits = ?, settings = ?, updated_at = ?
WHERE id = ?;

-- name: SetInstanceDesired :exec
UPDATE database_instances SET desired_state = ?, updated_at = ? WHERE id = ?;

-- name: DeleteInstance :exec
DELETE FROM database_instances WHERE id = ?;

-- name: ListInstanceAddresses :many
SELECT * FROM instance_addresses ORDER BY network_id, ip;

-- name: InsertInstanceAddress :exec
INSERT INTO instance_addresses (instance_id, network_id, ip) VALUES (?, ?, ?);

-- name: DeleteInstanceAddress :exec
DELETE FROM instance_addresses WHERE instance_id = ? AND network_id = ?;

-- name: ListDatabases :many
SELECT * FROM databases ORDER BY created_at, id;

-- name: ListServerDatabases :many
SELECT * FROM databases WHERE server_id = ? ORDER BY created_at, id;

-- name: GetDatabase :one
SELECT * FROM databases WHERE id = ?;

-- name: InsertDatabase :exec
INSERT INTO databases (id, instance_id, server_id, label, db_name, username, password, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetDatabasePassword :exec
UPDATE databases SET password = ? WHERE id = ?;

-- name: DeleteDatabase :exec
DELETE FROM databases WHERE id = ?;

-- name: ListServerIDs :many
SELECT id FROM servers;

-- name: SetDatabaseReadOnly :exec
UPDATE databases SET ro_username = ?, ro_password = ? WHERE id = ?;

-- name: SetInstanceVersion :exec
UPDATE database_instances
SET version = ?, image = ?, prev_version = ?, prev_image = ?, rollback_until = ?, updated_at = ?
WHERE id = ?;

-- name: SetInstancePatchTried :exec
UPDATE database_instances SET patch_tried = ? WHERE id = ?;
