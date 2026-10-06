-- name: AddAuditEvent :exec
INSERT INTO audit_log (org_id, user_id, actor, actor_id, action, target, ip, user_agent, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UserActivity :many
-- An account's own events, newest first, before a cursor (an event ID).
SELECT * FROM audit_log
WHERE user_id = @user_id AND org_id IS NULL AND (@before::uuid IS NULL OR id < @before::uuid)
ORDER BY id DESC
LIMIT @lim;

-- name: OrgAuditLog :many
SELECT a.*, u.email AS actor_email FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.org_id = @org_id AND (@before::uuid IS NULL OR a.id < @before::uuid)
ORDER BY a.id DESC
LIMIT @lim;

-- name: PruneAuditLog :exec
DELETE FROM audit_log WHERE at < now() - interval '1 year';

-- name: SessionsWithUserAgent :one
-- For new-device emails: the user's sessions so far, and those from this
-- browser.
SELECT count(*) AS total, count(*) FILTER (WHERE user_agent = @user_agent) AS same
FROM sessions WHERE user_id = @user_id;
