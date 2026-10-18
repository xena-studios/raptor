-- name: AddAuditEvent :exec
INSERT INTO audit_log (org_id, user_id, actor, actor_id, action, target, ip, user_agent, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UserActivity :many
-- An account's own events, newest first, before a cursor (an event ID),
-- narrowed by the filter (auth.Filter; unset parts are NULL or empty).
SELECT a.* FROM audit_log a
WHERE a.user_id = @user_id AND a.org_id IS NULL
  AND (sqlc.narg(before)::uuid IS NULL OR a.id < sqlc.narg(before)::uuid)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(ip)::text IS NULL OR a.ip <<= (sqlc.narg(ip)::text)::inet)
  AND (cardinality(@actions::text[]) = 0 OR a.action LIKE ANY (@actions::text[])
       OR a.metadata->>'action' LIKE ANY (@actions::text[]))
  AND (NOT @failed::bool OR a.action LIKE '%.failed' OR a.metadata->>'error' IS NOT NULL)
  AND (sqlc.narg(query)::text IS NULL OR concat_ws(' ', a.action, a.target, host(a.ip),
       a.user_agent, a.metadata::text) ILIKE sqlc.narg(query)::text)
ORDER BY a.id DESC
LIMIT @lim;

-- name: OrgAuditLog :many
-- An org's events, newest first, before a cursor, narrowed by the filter.
-- With server_id (and its node_id), only what was done to that server;
-- with node_id alone, what was done to the node and its servers.
SELECT a.*, u.email AS actor_email, s.email AS subject_email FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
LEFT JOIN users s ON s.id = a.user_id
WHERE a.org_id = @org_id
  AND (sqlc.narg(server_id)::text IS NULL
       OR (a.target = sqlc.narg(server_id)::text AND a.metadata->>'node' = sqlc.narg(node_id)::text))
  AND (sqlc.narg(server_id)::text IS NOT NULL OR sqlc.narg(node_id)::text IS NULL
       OR a.target = sqlc.narg(node_id)::text OR a.metadata->>'node' = sqlc.narg(node_id)::text)
  AND (sqlc.narg(before)::uuid IS NULL OR a.id < sqlc.narg(before)::uuid)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(ip)::text IS NULL OR a.ip <<= (sqlc.narg(ip)::text)::inet)
  AND (cardinality(@actions::text[]) = 0 OR a.action LIKE ANY (@actions::text[])
       OR a.metadata->>'action' LIKE ANY (@actions::text[]))
  AND (NOT @failed::bool OR a.action LIKE '%.failed' OR a.metadata->>'error' IS NOT NULL)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR a.actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(query)::text IS NULL OR concat_ws(' ', a.action, a.target, host(a.ip),
       a.user_agent, a.metadata::text, u.email, s.email) ILIKE sqlc.narg(query)::text)
ORDER BY a.id DESC
LIMIT @lim;

-- name: PruneAuditLog :exec
DELETE FROM audit_log WHERE at < now() - interval '1 year';

-- name: SessionsWithUserAgent :one
-- For new-device emails: the user's sessions so far, and those from this
-- browser.
SELECT count(*) AS total, count(*) FILTER (WHERE user_agent = @user_agent) AS same
FROM sessions WHERE user_id = @user_id;
