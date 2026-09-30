-- name: GetSFTPKey :one
SELECT * FROM sftp_key_cache WHERE username = ? AND server_id = ? AND fingerprint = ?;

-- name: UpsertSFTPKey :exec
INSERT INTO sftp_key_cache (username, server_id, fingerprint, public_key, user_id, permissions, confirmed_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (username, server_id, fingerprint) DO UPDATE SET
    public_key = excluded.public_key,
    user_id = excluded.user_id,
    permissions = excluded.permissions,
    confirmed_at = excluded.confirmed_at;

-- name: DeleteSFTPKey :exec
DELETE FROM sftp_key_cache WHERE username = ? AND server_id = ? AND fingerprint = ?;

-- name: PruneSFTPKeys :execrows
DELETE FROM sftp_key_cache WHERE confirmed_at < ?;
