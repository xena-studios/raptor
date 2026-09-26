-- name: AppendEvent :one
INSERT INTO events (type, server_id, version, at, data) VALUES (?, ?, ?, ?, ?) RETURNING seq;

-- name: EventsSince :many
SELECT * FROM events WHERE seq > ? ORDER BY seq LIMIT ?;

-- name: FirstEventSeq :one
SELECT CAST(COALESCE(MIN(seq), 0) AS INTEGER) FROM events;

-- name: LastEventSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM events;

-- name: PruneEvents :execrows
DELETE FROM events WHERE seq <= ?;
