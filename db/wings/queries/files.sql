-- name: CreateUpload :exec
INSERT INTO file_uploads (id, server_id, user_id, path, size, created_at, touched_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetUpload :one
SELECT * FROM file_uploads WHERE id = ?;

-- name: TouchUpload :exec
UPDATE file_uploads SET touched_at = ? WHERE id = ?;

-- name: DeleteUpload :exec
DELETE FROM file_uploads WHERE id = ?;

-- name: ListIdleUploads :many
SELECT * FROM file_uploads WHERE touched_at < ?;
