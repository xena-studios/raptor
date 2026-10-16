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
INSERT INTO nodes (org_id, name, public_key, facts, wings_version)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: NodeConnected :exec
-- A node on a new Wings version gets a fresh mirror snapshot (last_acked_seq
-- -1), so what a new version reports about servers shows up at once.
UPDATE nodes SET last_seen_at = now(), wings_version = $2, protocol_version = $3, system_read_at = NULL, capabilities = $4,
    last_acked_seq = CASE WHEN wings_version <> '' AND wings_version <> $2 THEN -1 ELSE last_acked_seq END
WHERE id = $1;

-- name: NodeSeen :exec
UPDATE nodes SET last_seen_at = now() WHERE id = $1;

-- name: RelinkNode :one
UPDATE nodes
SET public_key = $2, wings_version = $3, facts = $4, key_revoked_at = NULL, deleted_at = NULL
WHERE id = $1
RETURNING *;

-- name: SetNodeIPv4 :exec
UPDATE nodes SET public_ipv4 = $2 WHERE id = $1;

-- name: SetNodeIPv6 :exec
UPDATE nodes SET public_ipv6 = $2 WHERE id = $1;

-- name: NodeDNS :one
SELECT short_id, public_ipv4, public_ipv6, dns_ipv4, dns_ipv6 FROM nodes WHERE id = $1;

-- name: SetNodeDNS :exec
UPDATE nodes SET dns_ipv4 = $2, dns_ipv6 = $3 WHERE id = $1;

-- name: RetiredDNS :many
SELECT * FROM dns_retired ORDER BY name, type LIMIT 100;

-- name: ForgetRetiredDNS :exec
DELETE FROM dns_retired WHERE name = $1 AND type = $2;

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

-- name: SetNodeSFTP :exec
UPDATE nodes SET sftp_enabled = $2, sftp_port = $3, sftp_host_key = $4 WHERE id = $1;

-- name: SetJoinProgress :execrows
-- A bootstrap may still be running after its token expires (it started
-- before), so progress is taken for 3 hours after the token was made.
UPDATE join_tokens SET progress = @progress
WHERE token_hash = @token_hash AND created_at > now() - interval '3 hours';

-- name: GetJoinProgress :one
SELECT progress, node_id FROM join_tokens WHERE token_hash = @token_hash AND org_id = @org_id;

-- name: SetServerDisplayAddress :exec
INSERT INTO server_addresses (node_id, server_id, display_address) VALUES ($1, $2, $3)
ON CONFLICT (node_id, server_id) DO UPDATE SET display_address = excluded.display_address, updated_at = now();

-- name: ClearServerDisplayAddress :exec
DELETE FROM server_addresses WHERE node_id = $1 AND server_id = $2;

-- name: ForgetNodeServerAddresses :exec
DELETE FROM server_addresses WHERE node_id = $1;

-- name: MachinesToSync :many
-- Connected nodes whose settings haven't been read since they connected, or
-- whose hostname isn't what it should be.
SELECT n.id, n.name, n.short_id, n.hostname, n.hostname_custom, n.hostname_tried,
       (n.system_read_at IS NOT NULL)::bool AS read
FROM nodes n
WHERE n.deleted_at IS NULL AND n.key_revoked_at IS NULL
  AND EXISTS (SELECT 1 FROM node_connections c JOIN panel_instances i ON i.id = c.instance_id
              WHERE c.node_id = n.id AND i.seen_at > now() - interval '20 seconds');

-- name: SetNodeSystem :exec
-- What the machine reported. A hostname changed on the machine itself
-- (not by the Panel) is asked for again.
UPDATE nodes SET
    hostname = @hostname, timezone = @timezone, system_read_at = now(),
    hostname_tried = CASE WHEN hostname <> '' AND hostname <> @hostname THEN '' ELSE hostname_tried END
WHERE id = $1;

-- name: SetNodeTimezone :exec
UPDATE nodes SET timezone = $2 WHERE id = $1;

-- name: SetNodeHostnameTried :exec
-- The Panel asked the node for a hostname: what it is now if it took it,
-- or why not.
UPDATE nodes SET hostname_tried = @tried, hostname_error = @error,
    hostname = CASE WHEN @error::text = '' THEN @tried ELSE hostname END
WHERE id = $1;

-- name: SetNodeHostnameCustom :one
-- '' follows the node's name again. Clearing tried has it applied now.
UPDATE nodes SET hostname_custom = $2, hostname_tried = '', hostname_error = ''
WHERE id = $1 AND deleted_at IS NULL
RETURNING name, short_id;
