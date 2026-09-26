-- name: ClaimCommand :execrows
INSERT INTO executed_commands (command_id, payload_hash, action, user_id, signed_by, status, received_at)
VALUES (?, ?, ?, ?, ?, 'running', ?)
ON CONFLICT (command_id) DO NOTHING;

-- name: GetCommand :one
SELECT * FROM executed_commands WHERE command_id = ?;

-- name: FinishCommand :exec
UPDATE executed_commands SET status = ?, result = ?, error = ?, finished_at = ? WHERE command_id = ?;

-- name: PruneCommands :execrows
DELETE FROM executed_commands WHERE received_at < ? AND status != 'running';

-- name: InterruptedCommands :execrows
UPDATE executed_commands SET status = 'failed', error = 'interrupted: Wings stopped while running it', finished_at = ?
WHERE status = 'running';

-- name: ListTrustedKeys :many
SELECT * FROM trusted_keys ORDER BY added_at, credential_id;

-- name: GetTrustedKey :one
SELECT * FROM trusted_keys WHERE credential_id = ?;

-- name: InsertTrustedKey :exec
INSERT INTO trusted_keys (credential_id, user_id, public_key, role, server_id, actions, name, sign_count, expires_at, added_by, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?);

-- name: DeleteTrustedKey :execrows
DELETE FROM trusted_keys WHERE credential_id = ?;

-- name: CountOwnerKeys :one
SELECT COUNT(*) FROM trusted_keys WHERE role = 'owner';

-- name: SetSignCount :exec
UPDATE trusted_keys SET sign_count = ? WHERE credential_id = ?;
