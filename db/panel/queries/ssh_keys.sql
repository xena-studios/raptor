-- name: SetSFTPUsername :execrows
UPDATE users SET sftp_username = $2 WHERE id = $1 AND sftp_username IS NULL;

-- name: GetUserBySFTPUsername :one
SELECT * FROM users WHERE sftp_username = $1;

-- name: AddSSHKey :one
INSERT INTO ssh_keys (user_id, name, public_key, fingerprint) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListSSHKeys :many
SELECT * FROM ssh_keys WHERE user_id = $1 ORDER BY created_at;

-- name: CountSSHKeys :one
SELECT count(*) FROM ssh_keys WHERE user_id = $1;

-- name: DeleteSSHKey :one
DELETE FROM ssh_keys WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: SSHKeyByFingerprint :one
SELECT * FROM ssh_keys WHERE user_id = $1 AND fingerprint = $2;

-- name: UseSSHKey :exec
UPDATE ssh_keys SET last_used_at = now() WHERE id = $1;

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
