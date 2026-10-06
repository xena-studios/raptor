-- name: SetServerGrant :exec
INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions, granted_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (node_id, server_id, user_id)
DO UPDATE SET permissions = excluded.permissions, granted_by = excluded.granted_by, updated_at = now();

-- name: DeleteServerGrant :execrows
DELETE FROM server_grants WHERE node_id = $1 AND server_id = $2 AND user_id = $3;

-- name: ServerGrant :one
SELECT * FROM server_grants WHERE node_id = $1 AND server_id = $2 AND user_id = $3;

-- name: ServerGrants :many
SELECT g.*, u.email FROM server_grants g JOIN users u ON u.id = g.user_id
WHERE g.node_id = $1 AND g.server_id = $2
ORDER BY u.email;

-- name: NodeOrg :one
-- The org a live node belongs to.
SELECT org_id FROM nodes WHERE id = $1 AND deleted_at IS NULL;

-- name: MirroredServerExists :one
SELECT EXISTS (SELECT 1 FROM m_servers WHERE node_id = $1 AND server_id = $2);
