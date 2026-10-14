-- Player subdomains (docs/PANEL.md#subdomains).

-- name: OrgSubdomains :many
-- An org's subdomains, for its members (row-level security).
SELECT id, node_id, server_id, name, created_at, dns_target, dns_port, dns_error
FROM subdomains WHERE org_id = $1 AND released_at IS NULL ORDER BY name;

-- name: SubdomainBanned :one
SELECT EXISTS (SELECT 1 FROM subdomain_bans WHERE name = $1);

-- name: SubdomainHolder :one
-- Who holds a name now: the org using it, or the one that released it in
-- the last 30 days. No row: it's free.
SELECT org_id, released_at FROM subdomains
WHERE name = $1 AND (released_at IS NULL OR released_at > now() - interval '30 days');

-- name: CountOrgSubdomains :one
SELECT count(*) FROM subdomains WHERE org_id = $1 AND released_at IS NULL;

-- name: ClaimSubdomain :one
-- Claims a free name, or one released by the same org or over 30 days ago.
-- No row: someone else holds it. What DNS has stays recorded, so the next
-- sync rewrites only what differs.
INSERT INTO subdomains (org_id, node_id, server_id, name, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO UPDATE SET
    org_id = EXCLUDED.org_id, node_id = EXCLUDED.node_id, server_id = EXCLUDED.server_id,
    created_by = EXCLUDED.created_by, created_at = now(), released_at = NULL, dns_error = ''
WHERE subdomains.released_at IS NOT NULL
  AND (subdomains.org_id = EXCLUDED.org_id OR subdomains.released_at < now() - interval '30 days')
RETURNING id;

-- name: ReleaseSubdomain :one
-- An org removes one of its subdomains; its records go on the next sync.
UPDATE subdomains SET released_at = now()
WHERE id = $1 AND org_id = $2 AND released_at IS NULL
RETURNING id;

-- name: ReleaseSubdomainByName :one
-- Staff take a name away.
UPDATE subdomains SET released_at = now()
WHERE name = $1 AND released_at IS NULL
RETURNING id, org_id;

-- name: BanSubdomain :exec
INSERT INTO subdomain_bans (name, reason, staff_id) VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET reason = EXCLUDED.reason, staff_id = EXCLUDED.staff_id;

-- name: SubdomainsToSync :many
-- Every subdomain whose records may need writing or deleting: the ones in
-- use, with their node and server as the Panel knows them, and released
-- ones whose records are still there.
SELECT d.id, d.name, d.released_at, d.dns_target, d.dns_port,
       n.short_id, n.deleted_at AS node_deleted_at,
       s.config AS server_config, (s.server_id IS NOT NULL)::bool AS server_exists
FROM subdomains d
JOIN nodes n ON n.id = d.node_id
LEFT JOIN m_servers s ON s.node_id = d.node_id AND s.server_id = d.server_id
WHERE d.released_at IS NULL OR d.dns_target <> ''
ORDER BY d.id;

-- name: SetSubdomainDNS :exec
UPDATE subdomains SET dns_target = $2, dns_port = $3, dns_error = $4 WHERE id = $1;

-- name: ReleaseSubdomainGone :exec
-- Its server or node is gone.
UPDATE subdomains SET released_at = now() WHERE id = $1 AND released_at IS NULL;

-- name: MirrorServerExists :one
SELECT EXISTS (SELECT 1 FROM m_servers WHERE node_id = $1 AND server_id = $2);
