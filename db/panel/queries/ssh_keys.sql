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
