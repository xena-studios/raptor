-- name: CreateOAuthFlow :exec
INSERT INTO oauth_flows (state_hash, provider, verifier, nonce, session_id, expires_at) VALUES ($1, $2, $3, $4, $5, $6);

-- name: TakeOAuthFlow :one
DELETE FROM oauth_flows WHERE state_hash = $1 RETURNING *;

-- name: OAuthAccount :one
SELECT * FROM oauth_accounts WHERE provider = $1 AND subject = $2;

-- name: CreateOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, subject, email, email_verified, last_used_at)
VALUES ($1, $2, $3, $4, $5, now())
RETURNING *;

-- name: UseOAuthAccount :exec
UPDATE oauth_accounts SET email = $2, email_verified = $3, last_used_at = now() WHERE id = $1;

-- name: ListOAuthAccounts :many
SELECT * FROM oauth_accounts WHERE user_id = $1 ORDER BY created_at;

-- name: DeleteOAuthAccount :one
DELETE FROM oauth_accounts WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: PruneOAuthFlows :exec
DELETE FROM oauth_flows WHERE expires_at < now();
