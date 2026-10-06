-- name: InstanceAlive :exec
INSERT INTO panel_instances (id) VALUES ($1)
ON CONFLICT (id) DO UPDATE SET seen_at = now();

-- name: InstanceGone :exec
DELETE FROM panel_instances WHERE id = $1;

-- name: PruneInstances :exec
DELETE FROM panel_instances WHERE seen_at < now() - make_interval(secs => @stale_secs::float8);

-- name: SetNodeConnection :exec
INSERT INTO node_connections (node_id, instance_id) VALUES ($1, $2)
ON CONFLICT (node_id) DO UPDATE SET instance_id = EXCLUDED.instance_id, connected_at = now();

-- name: ClearNodeConnection :exec
DELETE FROM node_connections WHERE node_id = $1 AND instance_id = $2;

-- name: ClearInstanceConnections :exec
DELETE FROM node_connections WHERE instance_id = $1;

-- name: NodeHolder :one
-- The instance holding a node, if it's alive.
SELECT c.instance_id FROM node_connections c
JOIN panel_instances i ON i.id = c.instance_id
WHERE c.node_id = $1 AND i.seen_at > now() - make_interval(secs => @stale_secs::float8);

-- name: CreateNodeRequest :one
INSERT INTO node_requests (node_id, origin, target, method, request)
VALUES ($1, $2, $3, $4, $5) RETURNING id;

-- name: TakeNodeRequest :one
-- Claims a request, so a notification and the fallback poll can't both run it.
UPDATE node_requests SET claimed_at = now()
WHERE id = $1 AND target = $2 AND claimed_at IS NULL
RETURNING *;

-- name: AnswerNodeRequest :one
UPDATE node_requests SET response = $2, error_code = $3, error = $4, done_at = now()
WHERE id = $1 RETURNING origin;

-- name: GetNodeRequest :one
SELECT * FROM node_requests WHERE id = $1;

-- name: PruneNodeRequests :exec
DELETE FROM node_requests WHERE created_at < now() - interval '1 hour';

-- name: PendingNodeRequests :many
-- Requests for this instance that haven't been answered (a fallback for a
-- missed notification).
SELECT id FROM node_requests
WHERE target = $1 AND claimed_at IS NULL AND created_at < now() - interval '1 second';
