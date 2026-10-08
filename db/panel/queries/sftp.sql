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
