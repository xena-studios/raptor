-- The public demo (#449, docs/PANEL.md#the-public-demo).

-- name: DemoOrg :one
-- The org guests join: the oldest demo org that's usable.
SELECT id, name FROM orgs
WHERE demo AND deleted_at IS NULL AND suspended_at IS NULL
ORDER BY created_at
LIMIT 1;

-- name: DemoOrgs :many
SELECT id, demo_reset_at FROM orgs WHERE demo AND deleted_at IS NULL ORDER BY created_at;

-- name: SetOrgDemo :execrows
UPDATE orgs SET demo = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: SetDemoReset :exec
UPDATE orgs SET demo_reset_at = $2 WHERE id = $1;

-- name: OrgDemoRole :one
-- Whether an org is the demo org, and the caller's role in it.
SELECT o.demo, m.role FROM orgs o
JOIN org_members m ON m.org_id = o.id AND m.user_id = sqlc.arg(user_id)
WHERE o.id = sqlc.arg(org_id);

-- name: CreateGuest :one
INSERT INTO users (email, name, guest_until) VALUES ($1, 'Guest', $2) RETURNING *;

-- name: CountGuests :one
SELECT count(*) FROM users WHERE guest_until > now();

-- name: SetSessionExpiry :exec
UPDATE sessions SET expires_at = $2 WHERE id = $1;

-- name: AddDemoMember :exec
INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member');

-- name: DemoServers :many
-- An org's showcase servers on nodes it still has.
SELECT d.* FROM demo_servers d
JOIN nodes n ON n.id = d.node_id AND n.org_id = d.org_id AND n.deleted_at IS NULL
WHERE d.org_id = $1
ORDER BY d.node_id, d.server_id;

-- name: SetDemoServer :exec
INSERT INTO demo_servers (org_id, node_id, server_id, permissions, known_good_backup)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (node_id, server_id) DO UPDATE
SET org_id = excluded.org_id, permissions = excluded.permissions, known_good_backup = excluded.known_good_backup;

-- name: DeleteDemoServer :execrows
DELETE FROM demo_servers WHERE node_id = $1 AND server_id = $2;

-- name: PruneGuests :execrows
-- Guests whose hour is up; their sessions, memberships, and grants go
-- with them.
DELETE FROM users WHERE guest_until IS NOT NULL AND guest_until <= now();

-- name: DeleteOrgGuests :execrows
-- Every guest in an org, at its hourly reset.
DELETE FROM users WHERE guest_until IS NOT NULL
AND id IN (SELECT user_id FROM org_members WHERE org_id = $1);
