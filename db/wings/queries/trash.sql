-- name: InsertTrash :exec
INSERT INTO trash (id, server_id, path, is_dir, size, deleted_at, deleted_by) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListTrash :many
SELECT * FROM trash WHERE server_id = ? ORDER BY deleted_at DESC, id;

-- name: GetTrash :one
SELECT * FROM trash WHERE id = ? AND server_id = ?;

-- name: DeleteTrash :exec
DELETE FROM trash WHERE id = ?;

-- name: TrashBefore :many
SELECT * FROM trash WHERE deleted_at < ? ORDER BY deleted_at;
