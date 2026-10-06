-- name: CreateOrg :one
INSERT INTO orgs (name) VALUES ($1) RETURNING *;

-- name: CreateJoinToken :one
INSERT INTO join_tokens (org_id, token_hash, name, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetJoinTokenForUpdate :one
SELECT * FROM join_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: UseJoinToken :exec
UPDATE join_tokens SET used_at = now(), node_id = $2 WHERE id = $1;

-- name: CreateNode :one
INSERT INTO nodes (org_id, name, short_id, public_key, facts, wings_version)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: NodeConnected :exec
UPDATE nodes SET last_seen_at = now(), wings_version = $2, protocol_version = $3 WHERE id = $1;

-- name: NodeSeen :exec
UPDATE nodes SET last_seen_at = now() WHERE id = $1;

-- name: RelinkNode :one
UPDATE nodes
SET public_key = $2, wings_version = $3, facts = $4, key_revoked_at = NULL, deleted_at = NULL
WHERE id = $1
RETURNING *;

-- name: ShortIDTaken :one
SELECT EXISTS (SELECT 1 FROM nodes WHERE short_id = $1);
