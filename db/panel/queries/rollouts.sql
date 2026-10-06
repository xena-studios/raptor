-- name: CreateRollout :one
INSERT INTO wings_rollouts (version) VALUES ($1) RETURNING *;

-- name: ActiveRollout :one
SELECT * FROM wings_rollouts WHERE state IN ('running', 'paused');

-- name: LatestRollout :one
SELECT * FROM wings_rollouts ORDER BY created_at DESC LIMIT 1;

-- name: SetRolloutState :exec
UPDATE wings_rollouts
SET state = $2, reason = $3,
    finished_at = CASE WHEN $2 IN ('halted', 'done', 'cancelled') THEN now() END
WHERE id = $1;

-- name: AdvanceRollout :exec
UPDATE wings_rollouts SET stage = stage + 1, stage_started_at = now() WHERE id = $1;

-- name: RolloutNodes :many
SELECT * FROM rollout_nodes WHERE rollout_id = $1;

-- name: AddRolloutNode :exec
INSERT INTO rollout_nodes (rollout_id, node_id, stage, status, from_version, error, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $4 = 'sent' THEN NULL ELSE now() END);

-- name: FinishRolloutNode :exec
UPDATE rollout_nodes SET status = $3, error = $4, finished_at = now()
WHERE rollout_id = $1 AND node_id = $2;

-- name: RolloutCandidates :many
-- Every node that could be updated, with its version and whether an
-- instance holds its connection now.
SELECT n.id, n.wings_version,
       EXISTS (SELECT 1 FROM node_connections c JOIN panel_instances i ON i.id = c.instance_id
               WHERE c.node_id = n.id AND i.seen_at > now() - interval '20 seconds') AS connected
FROM nodes n
WHERE n.deleted_at IS NULL AND n.key_revoked_at IS NULL;
