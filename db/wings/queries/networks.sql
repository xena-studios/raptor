-- name: ListNetworks :many
SELECT * FROM networks ORDER BY created_at, id;

-- name: GetNetwork :one
SELECT * FROM networks WHERE id = ?;

-- name: InsertNetwork :exec
INSERT INTO networks (id, name, subnet, internal, ipv6, mtu, host_access, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateNetwork :exec
UPDATE networks
SET name = ?, subnet = ?, internal = ?, ipv6 = ?, mtu = ?, host_access = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteNetwork :exec
DELETE FROM networks WHERE id = ?;

-- name: SetServerNetworks :exec
UPDATE servers SET networks = ?, version = version + 1, updated_at = unixepoch() WHERE id = ?;

-- name: ListServerNetworks :many
SELECT id, name, networks FROM servers ORDER BY name, id;

-- name: ListServerAddresses :many
SELECT * FROM server_addresses ORDER BY network_id, ip;

-- name: InsertServerAddress :exec
INSERT INTO server_addresses (server_id, network_id, ip) VALUES (?, ?, ?);

-- name: DeleteServerAddress :exec
DELETE FROM server_addresses WHERE server_id = ? AND network_id = ?;

-- name: DeleteNetworkAddresses :exec
DELETE FROM server_addresses WHERE network_id = ?;
