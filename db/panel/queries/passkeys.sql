-- name: SetWebAuthnHandle :one
-- The user's passkey handle, made the first time it's needed.
UPDATE users SET webauthn_handle = COALESCE(webauthn_handle, @handle::bytea) WHERE id = $1
RETURNING webauthn_handle;

-- name: GetUserByWebAuthnHandle :one
SELECT * FROM users WHERE webauthn_handle = $1;

-- name: CreatePasskey :one
INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListPasskeys :many
SELECT * FROM passkeys WHERE user_id = $1 ORDER BY created_at;

-- name: CountPasskeys :one
SELECT count(*) FROM passkeys WHERE user_id = $1;

-- name: PasskeyByCredentialID :one
-- Locked: signing in updates the counter.
SELECT * FROM passkeys WHERE credential_id = $1 FOR UPDATE;

-- name: UsePasskey :exec
UPDATE passkeys SET credential = $2, last_used_at = now() WHERE id = $1;

-- name: RenamePasskey :execrows
UPDATE passkeys SET name = $3 WHERE id = $1 AND user_id = $2;

-- name: DeletePasskey :one
DELETE FROM passkeys WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: CreateCeremony :one
INSERT INTO webauthn_ceremonies (purpose, session_id, data, expires_at) VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: TakeCeremony :one
-- Ceremonies are single use: taking one deletes it.
DELETE FROM webauthn_ceremonies WHERE id = $1 RETURNING *;

-- name: PruneCeremonies :exec
DELETE FROM webauthn_ceremonies WHERE expires_at < now();
