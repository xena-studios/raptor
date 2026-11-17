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

-- Phases 2 and 3: sandboxes (docs/PANEL.md#sandboxes).

-- name: SetDemoNode :exec
INSERT INTO demo_nodes (node_id, org_id, max_sandboxes, port_min, port_max, warm_eggs)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (node_id) DO UPDATE
SET org_id = excluded.org_id, max_sandboxes = excluded.max_sandboxes, port_min = excluded.port_min,
    port_max = excluded.port_max, warm_eggs = excluded.warm_eggs;

-- name: DeleteDemoNode :execrows
DELETE FROM demo_nodes WHERE node_id = $1;

-- name: DemoNodes :many
-- The demo nodes of demo orgs, with what the Panel knows of them.
SELECT d.*, n.name, coalesce(n.facts->>'arch', '')::text AS arch, n.capabilities, n.short_id,
    EXISTS (SELECT 1 FROM node_connections c WHERE c.node_id = n.id)::bool AS connected
FROM demo_nodes d
JOIN nodes n ON n.id = d.node_id AND n.org_id = d.org_id AND n.deleted_at IS NULL
JOIN orgs o ON o.id = d.org_id AND o.demo AND o.deleted_at IS NULL
ORDER BY d.created_at;

-- name: DemoNodeOf :one
-- The demo node sandboxes in an org go on: its oldest.
SELECT d.*, n.name, coalesce(n.facts->>'arch', '')::text AS arch, n.capabilities, n.short_id,
    EXISTS (SELECT 1 FROM node_connections c WHERE c.node_id = n.id)::bool AS connected
FROM demo_nodes d
JOIN nodes n ON n.id = d.node_id AND n.org_id = d.org_id AND n.deleted_at IS NULL
WHERE d.org_id = $1
ORDER BY d.created_at
LIMIT 1;

-- name: SetDemoWarmed :exec
UPDATE demo_nodes SET warmed_on = $2 WHERE node_id = $1;

-- name: QueueSandbox :one
INSERT INTO demo_sandboxes (org_id, node_id, user_id, ip_hash, egg_id, library_id, egg_name, accept_eula, warm)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UserSandbox :one
-- A guest's live sandbox, or the one that ended last.
SELECT * FROM demo_sandboxes
WHERE user_id = $1 AND org_id = $2
ORDER BY (state IN ('queued', 'creating', 'installing', 'ready')) DESC, queued_at DESC
LIMIT 1;

-- name: LiveSandboxes :many
-- A node's sandboxes from the queue to their deletion, oldest first.
SELECT * FROM demo_sandboxes
WHERE node_id = $1 AND state IN ('queued', 'creating', 'installing', 'ready')
ORDER BY queued_at;

-- name: QueuePosition :one
-- How many queued sandboxes are ahead of one (0: it's next).
SELECT count(*) FROM demo_sandboxes
WHERE node_id = $1 AND state = 'queued' AND queued_at < $2;

-- name: SandboxCreating :execrows
UPDATE demo_sandboxes SET state = 'creating', port = $2, created_at = now()
WHERE id = $1 AND state = 'queued';

-- name: SandboxInstalling :exec
UPDATE demo_sandboxes SET state = 'installing', server_id = $2 WHERE id = $1;

-- name: SandboxReady :exec
UPDATE demo_sandboxes SET state = 'ready', ready_at = $2, deadline = $3, extended = $4
WHERE id = $1 AND state IN ('installing', 'ready');

-- name: SandboxDeadline :exec
UPDATE demo_sandboxes SET deadline = $2 WHERE id = $1;

-- name: EndSandbox :exec
-- A sandbox is over: deleted (gone) or never made (failed). The guest is
-- forgotten, and so is the address hash.
UPDATE demo_sandboxes SET state = $2, error = $3, ended_at = now(), ip_hash = ''
WHERE id = $1 AND state IN ('queued', 'creating', 'installing', 'ready');

-- name: EndOrphanSandboxes :execrows
-- Queued sandboxes of guests who are gone (at the hourly reset, or their
-- hour was up).
UPDATE demo_sandboxes SET state = 'failed', error = 'the guest left', ended_at = now(), ip_hash = ''
WHERE state = 'queued' AND user_id IS NULL AND NOT warm;

-- name: MostPickedEggs :many
-- The eggs guests picked most in the last month, for the warm pool.
SELECT library_id, egg_id, count(*)::int AS picks FROM demo_sandboxes
WHERE NOT warm AND org_id = $1 AND queued_at > now() - interval '30 days'
GROUP BY library_id, egg_id
ORDER BY picks DESC, library_id
LIMIT $2;

-- name: DemoLibraryEggs :many
-- The official library's published eggs, for the demo's picker.
SELECT id, catalog_id::text AS catalog_id, name, egg FROM eggs
WHERE org_id IS NULL AND catalog_id IS NOT NULL AND status = 'published';

-- name: DemoLibraryEgg :one
SELECT id, catalog_id::text AS catalog_id, name, egg FROM eggs
WHERE id = $1 AND org_id IS NULL AND catalog_id IS NOT NULL AND status = 'published';

-- name: NodeServerPorts :many
-- Every port a node's servers use, as the mirror knows them.
SELECT DISTINCT (a->>'port')::int AS port
FROM m_servers s, jsonb_array_elements(coalesce(s.config->'allocations', '[]')) a
WHERE s.node_id = $1;

-- name: IsDemoServer :one
-- Whether a server is one of the demo's showcase servers.
SELECT EXISTS (SELECT 1 FROM demo_servers WHERE node_id = $1 AND server_id = $2)::bool;
