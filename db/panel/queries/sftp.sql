-- name: SetSFTPPassword :one
-- One per user and server: turning it on again replaces the old one.
INSERT INTO sftp_passwords (user_id, node_id, server_id, username, secret_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, node_id, server_id) DO UPDATE
SET username = excluded.username, secret_hash = excluded.secret_hash,
    expires_at = excluded.expires_at, created_at = now()
RETURNING *;

-- name: GetSFTPPassword :one
SELECT * FROM sftp_passwords WHERE user_id = $1 AND node_id = $2 AND server_id = $3;

-- name: SFTPPasswordByUsername :one
SELECT * FROM sftp_passwords WHERE username = $1;

-- name: DeleteSFTPPassword :one
DELETE FROM sftp_passwords WHERE user_id = $1 AND node_id = $2 AND server_id = $3 RETURNING *;

-- name: PruneSFTPPasswords :exec
DELETE FROM sftp_passwords WHERE expires_at < now();

-- name: NodeSFTPWanted :one
-- Whether a node's SFTP port should be open: SFTP allowed there, and a
-- password on it that hasn't run out.
SELECT n.sftp_enabled,
       (n.sftp_allowed AND EXISTS (
           SELECT 1 FROM sftp_passwords p WHERE p.node_id = n.id AND p.expires_at > now()
       ))::bool AS wanted
FROM nodes n WHERE n.id = $1 AND n.deleted_at IS NULL;

-- name: NodesSFTPOutOfStep :many
-- Nodes whose SFTP port is open when it shouldn't be, or the other way.
SELECT n.id FROM nodes n
WHERE n.deleted_at IS NULL
  AND n.sftp_enabled <> (n.sftp_allowed AND EXISTS (
      SELECT 1 FROM sftp_passwords p WHERE p.node_id = n.id AND p.expires_at > now()
  ));

-- name: SetNodeSFTPAllowed :execrows
UPDATE nodes SET sftp_allowed = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: DeleteNodeSFTPPasswords :exec
DELETE FROM sftp_passwords WHERE node_id = $1;
