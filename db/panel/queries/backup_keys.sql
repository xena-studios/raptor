-- name: UpsertBackupKey :exec
INSERT INTO backup_keys (node_id, org_id, sealed, fingerprint) VALUES ($1, $2, $3, $4)
ON CONFLICT (node_id) DO UPDATE SET sealed = excluded.sealed, fingerprint = excluded.fingerprint, stored_at = now();

-- name: GetBackupKey :one
SELECT * FROM backup_keys WHERE node_id = $1;

-- name: DeleteBackupKey :exec
DELETE FROM backup_keys WHERE node_id = $1;

-- name: NodesWithoutBackupKey :many
-- Linked nodes whose key the Panel hasn't asked for yet.
SELECT n.id, n.org_id FROM nodes n
WHERE n.deleted_at IS NULL AND n.org_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM backup_keys k WHERE k.node_id = n.id);

-- name: RecoverySources :many
-- The org's nodes, removed ones included, with what's there to recover
-- their backups with: the Panel's copy of their key, and whether they had
-- Raptor Backup Storage (whose files aren't deleted yet).
SELECT n.id, n.name, n.deleted_at,
       EXISTS (SELECT 1 FROM backup_keys k WHERE k.node_id = n.id) AS key_kept,
       EXISTS (SELECT 1 FROM backup_storage s WHERE s.node_id = n.id AND s.purged_at IS NULL) AS has_storage
FROM nodes n
WHERE n.org_id = $1
ORDER BY n.deleted_at IS NOT NULL, n.name;
