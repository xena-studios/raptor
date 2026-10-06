-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (email, email_verified_at) VALUES ($1, now()) RETURNING *;

-- name: VerifyUserEmail :exec
UPDATE users SET email_verified_at = now() WHERE id = $1 AND email_verified_at IS NULL;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent, reauth_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: SetSessionReauth :exec
UPDATE sessions SET reauth_at = $2 WHERE id = $1;

-- name: SessionByToken :one
SELECT * FROM sessions WHERE token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: RevokeOtherSessions :exec
UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL;

-- name: ListUserSessions :many
SELECT * FROM sessions
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
ORDER BY last_seen_at DESC;

-- name: CreateEmailCode :exec
INSERT INTO email_codes (email, code_hash, link_token_hash, expires_at, purpose) VALUES ($1, $2, $3, $4, $5);

-- name: LatestEmailCode :one
-- The newest unused code for an address and purpose, locked for checking.
SELECT * FROM email_codes WHERE email = $1 AND purpose = $2 AND used_at IS NULL
ORDER BY created_at DESC LIMIT 1
FOR UPDATE;

-- name: EmailCodeByLink :one
-- Only sign-in emails have links.
SELECT * FROM email_codes WHERE link_token_hash = $1 AND purpose = 'signin' FOR UPDATE;

-- name: CountEmailCodeAttempt :exec
UPDATE email_codes SET attempts = attempts + 1 WHERE id = $1;

-- name: UseEmailCode :exec
UPDATE email_codes SET used_at = now() WHERE id = $1;

-- name: AddRateEvent :exec
INSERT INTO rate_events (key) VALUES ($1);

-- name: CountRateEvents :one
SELECT count(*) FROM rate_events WHERE key = $1 AND at > now() - make_interval(secs => @window_secs::float8);

-- name: PruneRateEvents :exec
DELETE FROM rate_events WHERE at < now() - interval '1 day';

-- name: PruneEmailCodes :exec
DELETE FROM email_codes WHERE expires_at < now() - interval '1 day';

-- name: PruneSessions :exec
DELETE FROM sessions WHERE expires_at < now() - interval '30 days' OR revoked_at < now() - interval '30 days';
