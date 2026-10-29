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
-- A server's linked databases, in the order they were linked.
SELECT d.* FROM databases d JOIN database_links l ON l.database_id = d.id
WHERE l.server_id = ? ORDER BY l.created_at, d.id;

-- name: GetDatabase :one
SELECT * FROM databases WHERE id = ?;

-- name: InsertDatabase :exec
INSERT INTO databases (id, instance_id, label, db_name, username, password, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DatabaseLinked :one
SELECT COUNT(*) FROM database_links WHERE database_id = ? AND server_id = ?;

-- name: ListDatabaseLinks :many
SELECT * FROM database_links ORDER BY created_at, database_id, server_id;

-- name: InsertDatabaseLink :exec
INSERT INTO database_links (database_id, server_id, created_at) VALUES (?, ?, ?);

-- name: DeleteDatabaseLink :execrows
DELETE FROM database_links WHERE database_id = ? AND server_id = ?;

-- name: DeleteServerLinks :exec
DELETE FROM database_links WHERE server_id = ?;

-- name: SetDatabasePassword :exec
UPDATE databases SET password = ? WHERE id = ?;

-- name: DeleteDatabase :exec
DELETE FROM databases WHERE id = ?;

-- name: ListServerIDs :many
SELECT id FROM servers;

-- name: SetDatabaseReadOnly :exec
UPDATE databases SET ro_username = ?, ro_password = ? WHERE id = ?;

-- name: SetDatabaseViewer :exec
UPDATE databases SET viewer_username = ?, viewer_password = ? WHERE id = ?;

-- name: SetInstanceVersion :exec
UPDATE database_instances
SET version = ?, image = ?, prev_version = ?, prev_image = ?, rollback_until = ?, updated_at = ?
WHERE id = ?;

-- name: SetInstancePatchTried :exec
UPDATE database_instances SET patch_tried = ? WHERE id = ?;

-- name: SetInstanceExternal :exec
UPDATE database_instances SET external_port = ?, external_allow = ?, updated_at = ? WHERE id = ?;

-- name: ListInstanceExternalPorts :many
SELECT id, name, external_port FROM database_instances WHERE external_port > 0 ORDER BY external_port;
