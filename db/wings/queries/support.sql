-- name: InsertSupportGrant :exec
INSERT INTO support_grants (id, staff_id, staff_email, level, reason, ticket, granted_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSupportGrant :one
SELECT * FROM support_grants WHERE id = ?;

-- name: ListSupportGrants :many
SELECT * FROM support_grants ORDER BY created_at DESC LIMIT 50;

-- name: RevokeSupportGrant :execrows
UPDATE support_grants SET revoked_at = ?, revoked_by = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeAllSupportGrants :execrows
UPDATE support_grants SET revoked_at = ?, revoked_by = ? WHERE revoked_at IS NULL AND expires_at > ?;
