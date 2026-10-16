-- name: AddOrgMember :exec
INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO NOTHING;

-- name: OrgMember :one
SELECT * FROM org_members WHERE org_id = $1 AND user_id = $2;

-- name: UserOrgs :many
SELECT o.id, o.name, o.created_at, m.role, (o.suspended_at IS NOT NULL)::bool AS suspended FROM orgs o
JOIN org_members m ON m.org_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at;

-- name: CountOwnedOrgs :one
SELECT count(*) FROM org_members WHERE user_id = $1 AND role = 'owner';

-- name: GetOrg :one
SELECT * FROM orgs WHERE id = $1;

-- name: RenameOrg :exec
UPDATE orgs SET name = $2 WHERE id = $1;

-- name: OrgMembers :many
SELECT m.user_id, m.role, m.created_at, u.email, u.name FROM org_members m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1
ORDER BY m.created_at;

-- name: LockOrgOwners :many
-- The org's owners, locked, so two changes can't remove the last one
-- between them.
SELECT user_id FROM org_members WHERE org_id = $1 AND role = 'owner' FOR UPDATE;

-- name: SetOrgMemberRole :execrows
UPDATE org_members SET role = $3 WHERE org_id = $1 AND user_id = $2;

-- name: RemoveOrgMember :execrows
DELETE FROM org_members WHERE org_id = $1 AND user_id = $2;

-- name: CreateInvitation :one
INSERT INTO org_invitations (org_id, email, role, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: PendingInvitations :many
SELECT * FROM org_invitations
WHERE org_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at;

-- name: CountPendingInvitations :one
SELECT count(*) FROM org_invitations
WHERE org_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: InvitationByToken :one
SELECT * FROM org_invitations WHERE token_hash = $1 FOR UPDATE;

-- name: AcceptInvitation :exec
UPDATE org_invitations SET accepted_at = now() WHERE id = $1;

-- name: RevokeInvitation :execrows
UPDATE org_invitations SET revoked_at = now()
WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: OrgNodes :many
SELECT n.id, n.name, n.short_id, n.wings_version, n.last_seen_at, n.created_at,
       (c.node_id IS NOT NULL)::bool AS connected,
       coalesce(n.facts->>'arch', '')::text AS arch,
       coalesce((n.facts->>'cpus')::int, 0)::int AS cpus,
       coalesce((n.facts->>'memory_bytes')::bigint, 0)::bigint AS memory_bytes,
       n.sftp_enabled, n.sftp_port, n.sftp_host_key, n.sftp_allowed,
       n.hostname, n.hostname_custom, n.hostname_error, n.timezone,
       coalesce(host(n.public_ipv4), '')::text AS public_ipv4,
       coalesce(host(n.public_ipv6), '')::text AS public_ipv6
FROM nodes n LEFT JOIN node_connections c ON c.node_id = n.id
WHERE n.org_id = $1 AND n.deleted_at IS NULL
ORDER BY n.created_at;

-- name: NodeServers :many
SELECT s.server_id, s.name, s.state, s.egg_name, s.install_state, s.install_error,
       coalesce(s.config->'allocations', '[]')::jsonb AS allocations,
       coalesce(a.display_address, '')::text AS display_address
FROM m_servers s
LEFT JOIN server_addresses a ON a.node_id = s.node_id AND a.server_id = s.server_id
WHERE s.node_id = $1 ORDER BY s.name, s.server_id;

-- name: NodeServer :one
SELECT s.server_id, s.name, s.state, s.egg_name, s.install_state, s.install_error,
       coalesce(s.config->'allocations', '[]')::jsonb AS allocations, s.config,
       coalesce(a.display_address, '')::text AS display_address
FROM m_servers s
LEFT JOIN server_addresses a ON a.node_id = s.node_id AND a.server_id = s.server_id
WHERE s.node_id = $1 AND s.server_id = $2;

-- name: UserGrantsInOrg :many
-- A member's server grants in an org.
SELECT node_id, server_id, permissions FROM server_grants WHERE org_id = $1 AND user_id = $2;

-- name: CountOrgMembers :one
SELECT count(*) FROM org_members WHERE org_id = $1;

-- name: CountOrgNodes :one
-- The org's nodes that haven't been removed.
SELECT count(*) FROM nodes WHERE org_id = $1 AND deleted_at IS NULL;

-- name: RevokeOrgInvitations :exec
UPDATE org_invitations SET revoked_at = now()
WHERE org_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL;
