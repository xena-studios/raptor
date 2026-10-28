-- Staff insights (docs/PANEL.md#staff): the overview's tabs.

-- name: UserInsightCounts :one
SELECT
    (SELECT count(*) FROM users)::int AS users,
    (SELECT count(DISTINCT user_id) FROM sessions WHERE last_seen_at > now() - interval '1 day')::int AS active_1d,
    (SELECT count(DISTINCT user_id) FROM sessions WHERE last_seen_at > now() - interval '7 days')::int AS active_7d,
    (SELECT count(DISTINCT user_id) FROM sessions WHERE last_seen_at > now() - interval '30 days')::int AS active_30d,
    (SELECT count(DISTINCT user_id) FROM passkeys)::int AS with_passkey,
    (SELECT count(*) FROM users WHERE totp_enabled_at IS NOT NULL)::int AS with_totp,
    (SELECT count(*) FROM users WHERE suspended_at IS NOT NULL)::int AS suspended,
    (SELECT count(*) FROM users u WHERE NOT EXISTS (SELECT 1 FROM org_members m WHERE m.user_id = u.id))::int AS without_org,
    (SELECT count(*) FROM org_invitations WHERE accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now())::int AS open_invitations;

-- name: SigninMethods :many
-- How people signed in, in the last 30 days.
SELECT coalesce(metadata->>'method', 'unknown')::text AS method, count(*)::int AS n
FROM audit_log WHERE action = 'signin' AND at > now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;

-- name: OAuthProviders :many
SELECT provider, count(*)::int AS n FROM oauth_accounts GROUP BY 1 ORDER BY 2 DESC;

-- name: ActiveUsersByDay :many
-- Distinct accounts that signed in each day.
SELECT date_trunc('day', at)::date AS day, count(DISTINCT user_id)::int AS n
FROM audit_log WHERE action = 'signin' AND at >= $1 AND user_id IS NOT NULL GROUP BY 1 ORDER BY 1;

-- name: LargestOrgs :many
SELECT o.id, o.name,
       (SELECT count(*) FROM org_members m WHERE m.org_id = o.id)::int AS members,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS nodes,
       (SELECT count(*) FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS servers
FROM orgs o ORDER BY servers DESC, nodes DESC, members DESC LIMIT 10;

-- name: NodeInsightCounts :one
SELECT
    (SELECT count(*) FROM nodes WHERE deleted_at IS NULL)::int AS nodes,
    (SELECT count(*) FROM node_connections c JOIN nodes n ON n.id = c.node_id WHERE n.deleted_at IS NULL)::int AS connected,
    (SELECT coalesce(sum((facts->>'cpus')::int), 0) FROM nodes WHERE deleted_at IS NULL)::int AS cpus,
    (SELECT coalesce(sum((facts->>'memory_bytes')::bigint), 0) FROM nodes WHERE deleted_at IS NULL)::bigint AS memory_bytes,
    (SELECT count(*) FROM nodes WHERE deleted_at IS NULL AND created_at > now() - interval '30 days')::int AS linked_30d,
    (SELECT count(*) FROM nodes WHERE deleted_at IS NOT NULL)::int AS removed,
    (SELECT count(*) FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.deleted_at IS NULL)::int AS servers;

-- name: NodeOSes :many
SELECT coalesce(nullif(facts->>'os', ''), 'unknown')::text AS os, count(*)::int AS n
FROM nodes WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC;

-- name: ServersPerNode :many
-- How many nodes run how many servers: 0, 1, 2–5, 6–10, more.
SELECT CASE WHEN c = 0 THEN '0' WHEN c = 1 THEN '1' WHEN c <= 5 THEN '2–5' WHEN c <= 10 THEN '6–10' ELSE 'More than 10' END::text AS bucket,
       count(*)::int AS n
FROM (SELECT n.id, (SELECT count(*) FROM m_servers s WHERE s.node_id = n.id) AS c FROM nodes n WHERE n.deleted_at IS NULL) x
GROUP BY 1 ORDER BY min(c);

-- name: ServerStates :many
SELECT coalesce(nullif(s.state, ''), 'unknown')::text AS state, count(*)::int AS n
FROM m_servers s JOIN nodes n ON n.id = s.node_id WHERE n.deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC;

-- name: StorageUsageByDay :many
SELECT day, sum(bytes)::bigint AS bytes FROM backup_storage_usage WHERE day >= $1 GROUP BY day ORDER BY day;

-- name: StorageOrgs :many
SELECT o.id, o.name,
       (SELECT count(*) FROM backup_storage b WHERE b.org_id = o.id AND b.disabled_at IS NULL)::int AS nodes,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS org_nodes,
       coalesce((SELECT u.bytes FROM backup_storage_usage u WHERE u.org_id = o.id ORDER BY u.day DESC LIMIT 1), 0)::bigint AS bytes
FROM orgs o
WHERE EXISTS (SELECT 1 FROM backup_storage b WHERE b.org_id = o.id AND (b.disabled_at IS NULL OR b.purged_at IS NULL))
ORDER BY bytes DESC LIMIT 50;

-- name: PanelInstances :many
SELECT i.id, i.started_at, i.seen_at,
       (SELECT count(*) FROM node_connections c WHERE c.instance_id = i.id)::int AS nodes
FROM panel_instances i WHERE i.seen_at > now() - interval '10 minutes' ORDER BY i.started_at;

-- name: DatabaseSize :one
SELECT pg_database_size(current_database())::bigint;

-- name: SessionCounts :one
SELECT
    (SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > now())::int AS live,
    (SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND last_seen_at > now() - interval '1 hour')::int AS last_hour;
