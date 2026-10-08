-- name: CreateOrg :one
INSERT INTO orgs (name) VALUES ($1) RETURNING *;

-- name: CreateJoinToken :one
INSERT INTO join_tokens (org_id, token_hash, name, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetJoinTokenForUpdate :one
SELECT * FROM join_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: UseJoinToken :exec
UPDATE join_tokens SET used_at = now(), node_id = $2 WHERE id = $1;

-- name: CreateNode :one
INSERT INTO nodes (org_id, name, short_id, public_key, facts, wings_version)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: NodeConnected :exec
-- A node on a new Wings version gets a fresh mirror snapshot (last_acked_seq
-- -1), so what a new version reports about servers shows up at once.
UPDATE nodes SET last_seen_at = now(), wings_version = $2, protocol_version = $3,
    last_acked_seq = CASE WHEN wings_version <> '' AND wings_version <> $2 THEN -1 ELSE last_acked_seq END
WHERE id = $1;

-- name: NodeSeen :exec
UPDATE nodes SET last_seen_at = now() WHERE id = $1;

-- name: RelinkNode :one
UPDATE nodes
SET public_key = $2, wings_version = $3, facts = $4, key_revoked_at = NULL, deleted_at = NULL
WHERE id = $1
RETURNING *;

-- name: ShortIDTaken :one
SELECT EXISTS (SELECT 1 FROM nodes WHERE short_id = $1);

-- name: SetNodeIPv4 :exec
UPDATE nodes SET public_ipv4 = $2 WHERE id = $1;

-- name: SetNodeIPv6 :exec
UPDATE nodes SET public_ipv6 = $2 WHERE id = $1;

-- name: NodeDNS :one
SELECT short_id, public_ipv4, public_ipv6, dns_ipv4, dns_ipv6 FROM nodes WHERE id = $1;

-- name: SetNodeDNS :exec
UPDATE nodes SET dns_ipv4 = $2, dns_ipv6 = $3 WHERE id = $1;

-- name: SetJoinTokenPin :execrows
-- Only on an org's own unused, unexpired token, and only once.
UPDATE join_tokens SET owner_pin = $3
WHERE token_hash = $1 AND org_id = $2 AND used_at IS NULL AND expires_at > now() AND owner_pin IS NULL;

-- name: RemoveNode :execrows
-- Removing a node: its key is refused from now on (relinking with a join
-- token brings it back), and it's hidden everywhere.
UPDATE nodes SET deleted_at = now(), key_revoked_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- A removed node's mirror and members' grants: they were about servers the
-- Panel no longer manages.

-- name: ForgetNodeJobs :exec
DELETE FROM m_jobs WHERE node_id = $1;

-- name: ForgetNodeBackups :exec
DELETE FROM m_backups WHERE node_id = $1;

-- name: ForgetNodeSchedules :exec
DELETE FROM m_schedules WHERE node_id = $1;

-- name: ForgetNodeServers :exec
DELETE FROM m_servers WHERE node_id = $1;

-- name: ForgetNodeGrants :exec
DELETE FROM server_grants WHERE node_id = $1;

-- name: RenameNode :execrows
UPDATE nodes SET name = $2 WHERE id = $1 AND deleted_at IS NULL;
