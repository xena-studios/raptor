-- name: StaffRole :one
SELECT role FROM staff WHERE user_id = $1;

-- name: AddStaff :exec
INSERT INTO staff (user_id, role, added_by) VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET role = excluded.role, added_by = excluded.added_by, added_at = now();

-- name: RemoveStaff :execrows
DELETE FROM staff WHERE user_id = $1;

-- name: ListStaff :many
SELECT s.user_id, s.role, s.added_at, s.added_by, u.email, u.name
FROM staff s JOIN users u ON u.id = s.user_id ORDER BY s.added_at;

-- name: AddStaffAudit :exec
INSERT INTO staff_audit (staff_id, staff_email, action, target_type, target_id, meta, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListStaffAudit :many
SELECT * FROM staff_audit WHERE (sqlc.narg(before)::timestamptz IS NULL OR at < sqlc.narg(before))
ORDER BY at DESC LIMIT 50;

-- name: SetSessionPasskey :exec
UPDATE sessions SET passkey_at = $2 WHERE id = $1;

-- name: PlatformCounts :one
SELECT
    (SELECT count(*) FROM users)::int AS users,
    (SELECT count(*) FROM orgs WHERE deleted_at IS NULL)::int AS orgs,
    (SELECT count(*) FROM nodes WHERE deleted_at IS NULL)::int AS nodes,
    (SELECT count(*) FROM node_connections c JOIN nodes n ON n.id = c.node_id WHERE n.deleted_at IS NULL)::int AS nodes_connected,
    (SELECT count(*) FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.deleted_at IS NULL)::int AS servers,
    (SELECT count(*) FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.deleted_at IS NULL AND s.state = 'running')::int AS servers_running,
    (SELECT coalesce(sum(u.bytes), 0) FROM backup_storage_usage u
       WHERE u.day = (SELECT max(day) FROM backup_storage_usage x WHERE x.org_id = u.org_id))::bigint AS storage_bytes,
    (SELECT count(*) FROM users WHERE created_at > now() - interval '30 days')::int AS signups_30d,
    (SELECT count(DISTINCT user_id) FROM sessions WHERE last_seen_at > now() - interval '30 days')::int AS active_users_30d,
    (SELECT count(*) FROM backup_storage WHERE disabled_at IS NULL)::int AS storage_nodes;

-- name: InsertPlatformStats :exec
INSERT INTO platform_stats (at, users, orgs, nodes, nodes_connected, servers, servers_running, storage_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (at) DO NOTHING;

-- name: ListPlatformStats :many
SELECT * FROM platform_stats WHERE at >= $1 ORDER BY at;

-- name: SignupsByDay :many
SELECT date_trunc('day', created_at)::date AS day, count(*)::int AS n
FROM users WHERE created_at >= $1 GROUP BY 1 ORDER BY 1;

-- name: WingsVersions :many
SELECT coalesce(nullif(wings_version, ''), 'unknown')::text AS version, count(*)::int AS n
FROM nodes WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC, 1;

-- name: NodeArchs :many
SELECT coalesce(nullif(facts->>'arch', ''), 'unknown')::text AS arch, count(*)::int AS n
FROM nodes WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC;

-- name: TopEggs :many
SELECT coalesce(nullif(s.egg_name, ''), 'unknown')::text AS egg, count(*)::int AS n
FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.deleted_at IS NULL
GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10;

-- name: OfflineNodes :many
-- Nodes that have been gone over an hour, most recently seen first.
SELECT n.id, n.name, n.org_id, o.name AS org_name, n.last_seen_at, n.wings_version
FROM nodes n JOIN orgs o ON o.id = n.org_id
LEFT JOIN node_connections c ON c.node_id = n.id
WHERE n.deleted_at IS NULL AND c.node_id IS NULL AND n.last_seen_at < now() - interval '1 hour'
ORDER BY n.last_seen_at DESC NULLS LAST LIMIT 50;

-- name: FailingServers :many
SELECT s.node_id, s.server_id, s.name, s.state, s.install_state, s.install_error, s.egg_name, n.org_id, o.name AS org_name
FROM m_servers s JOIN nodes n ON n.id = s.node_id JOIN orgs o ON o.id = n.org_id
WHERE n.deleted_at IS NULL AND (s.state IN ('crashed', 'install_failed') OR s.install_state = 'failed')
ORDER BY s.updated_at DESC LIMIT 50;

-- name: SearchUsers :many
SELECT u.id, u.email, u.name, u.created_at, u.suspended_at,
       (SELECT count(*) FROM org_members m WHERE m.user_id = u.id)::int AS orgs,
       (SELECT count(*) FROM passkeys p WHERE p.user_id = u.id)::int AS passkeys,
       (SELECT max(last_seen_at) FROM sessions s WHERE s.user_id = u.id)::timestamptz AS last_seen_at,
       coalesce((SELECT role FROM staff st WHERE st.user_id = u.id), '')::text AS staff_role
FROM users u
WHERE @query::text = '' OR u.email ILIKE '%' || @query || '%' OR u.name ILIKE '%' || @query || '%' OR u.id::text = @query
ORDER BY u.created_at DESC LIMIT 50 OFFSET @skip;

-- name: UserMemberships :many
SELECT o.id, o.name, m.role, o.suspended_at FROM org_members m JOIN orgs o ON o.id = m.org_id
WHERE m.user_id = $1 ORDER BY o.name;

-- name: UserSignins :many
SELECT at, ip, user_agent, metadata FROM audit_log
WHERE user_id = $1 AND action = 'signin' ORDER BY at DESC LIMIT 10;

-- name: SuspendUser :execrows
UPDATE users SET suspended_at = now(), suspended_reason = $2 WHERE id = $1 AND suspended_at IS NULL;

-- name: UnsuspendUser :execrows
UPDATE users SET suspended_at = NULL, suspended_reason = '' WHERE id = $1;

-- name: RevokeUserSessions :execrows
UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: SearchOrgs :many
SELECT o.id, o.name, o.created_at, o.suspended_at,
       (SELECT count(*) FROM org_members m WHERE m.org_id = o.id)::int AS members,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS nodes,
       (SELECT count(*) FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS servers
FROM orgs o
WHERE @query::text = '' OR o.name ILIKE '%' || @query || '%' OR o.id::text = @query
ORDER BY o.created_at DESC LIMIT 50 OFFSET @skip;

-- name: GetOrgForStaff :one
SELECT * FROM orgs WHERE id = $1;

-- name: OrgMembersForStaff :many
SELECT u.id, u.email, u.name, m.role FROM org_members m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 ORDER BY m.role, u.email;

-- name: OrgNodesForStaff :many
SELECT n.id, n.name, n.short_id, n.wings_version, n.last_seen_at, n.created_at,
       (c.node_id IS NOT NULL)::bool AS connected,
       coalesce(n.facts->>'arch', '')::text AS arch,
       (SELECT count(*) FROM m_servers s WHERE s.node_id = n.id)::int AS servers
FROM nodes n LEFT JOIN node_connections c ON c.node_id = n.id
WHERE n.org_id = $1 AND n.deleted_at IS NULL ORDER BY n.created_at;

-- name: SuspendOrg :execrows
UPDATE orgs SET suspended_at = now(), suspended_reason = $2 WHERE id = $1 AND suspended_at IS NULL;

-- name: UnsuspendOrg :execrows
UPDATE orgs SET suspended_at = NULL, suspended_reason = '' WHERE id = $1;

-- name: StorageByOrg :many
-- Each org's latest measured Raptor Backup Storage use and how many of its
-- nodes have it on, for what a month would bill.
SELECT b.org_id,
       count(*)::int AS nodes,
       (SELECT count(*) FROM nodes n WHERE n.org_id = b.org_id AND n.deleted_at IS NULL)::int AS org_nodes,
       coalesce((SELECT u.bytes FROM backup_storage_usage u WHERE u.org_id = b.org_id ORDER BY u.day DESC LIMIT 1), 0)::bigint AS bytes
FROM backup_storage b WHERE b.disabled_at IS NULL GROUP BY b.org_id;

-- name: OrgSuspended :one
SELECT (suspended_at IS NOT NULL)::bool FROM orgs WHERE id = $1;

-- name: RecentStaffView :one
-- Whether this staff member opened this account or org in the last 10
-- minutes (a view is logged once, not on every refresh).
SELECT EXISTS (
    SELECT 1 FROM staff_audit WHERE staff_id = $1 AND action = $2 AND target_id = $3 AND at > now() - interval '10 minutes'
)::bool;

-- name: ActiveAnnouncements :many
SELECT * FROM announcements WHERE starts_at <= now() AND (ends_at IS NULL OR ends_at > now())
ORDER BY CASE level WHEN 'maintenance' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, starts_at DESC;

-- name: ListAnnouncements :many
SELECT * FROM announcements ORDER BY created_at DESC LIMIT 100;

-- name: CreateAnnouncement :one
INSERT INTO announcements (level, title, body, link, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateAnnouncement :execrows
UPDATE announcements SET level = $2, title = $3, body = $4, link = $5, starts_at = $6, ends_at = $7, updated_at = now()
WHERE id = $1;

-- name: EndAnnouncement :execrows
UPDATE announcements SET ends_at = now(), updated_at = now() WHERE id = $1 AND (ends_at IS NULL OR ends_at > now());
