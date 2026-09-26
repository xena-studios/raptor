-- name: InsertServer :exec
INSERT INTO servers (id, name, egg, egg_source, egg_hash, image, startup, variables, limits, settings,
                     host_network, desired_state, install_state, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, unixepoch(), unixepoch());

-- name: GetServer :one
SELECT * FROM servers WHERE id = ?;

-- name: ListServers :many
SELECT * FROM servers ORDER BY created_at, id;

-- name: UpdateServerConfig :exec
UPDATE servers
SET name = ?, image = ?, startup = ?, variables = ?, limits = ?, settings = ?, host_network = ?,
    version = version + 1, updated_at = unixepoch()
WHERE id = ?;

-- name: ReplaceEgg :exec
UPDATE servers
SET egg = ?, egg_source = ?, egg_hash = ?, image = ?, startup = ?, version = version + 1, updated_at = unixepoch()
WHERE id = ?;

-- name: SetDesiredState :exec
UPDATE servers SET desired_state = ?, updated_at = unixepoch() WHERE id = ?;

-- name: SetInstallState :exec
UPDATE servers SET install_state = ?, install_error = ?, updated_at = unixepoch() WHERE id = ?;

-- name: DeleteServer :exec
DELETE FROM servers WHERE id = ?;

-- name: ListAllocations :many
SELECT * FROM allocations ORDER BY server_id, is_primary DESC, ip, port;

-- name: ListServerAllocations :many
SELECT * FROM allocations WHERE server_id = ? ORDER BY is_primary DESC, ip, port;

-- name: InsertAllocation :exec
INSERT INTO allocations (server_id, ip, port, is_primary) VALUES (?, ?, ?, ?);

-- name: DeleteServerAllocations :exec
DELETE FROM allocations WHERE server_id = ?;

-- name: SetLastState :exec
UPDATE servers SET last_state = ? WHERE id = ?;
