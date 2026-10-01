-- name: InsertAudit :one
INSERT INTO audit_log (at, command_id, action, server_id, user_id, credential_id, key_name, command_hash, outcome, detail)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: FinishAudit :exec
UPDATE audit_log SET outcome = ?, detail = ? WHERE id = ?;

-- name: ListAudit :many
SELECT * FROM audit_log WHERE at >= sqlc.arg(since) ORDER BY id DESC LIMIT sqlc.arg(limit);

-- name: PruneAudit :execrows
DELETE FROM audit_log WHERE at < ?;

-- name: DeleteAllTrustedKeys :execrows
DELETE FROM trusted_keys;

-- name: GetAudit :one
SELECT * FROM audit_log WHERE id = ?;

-- name: InterruptedAudit :execrows
UPDATE audit_log SET outcome = 'failed', detail = 'interrupted: Wings stopped while running it' WHERE outcome = 'running';

-- name: ListAuditAfter :many
SELECT * FROM audit_log WHERE id > ? ORDER BY id LIMIT ?;
