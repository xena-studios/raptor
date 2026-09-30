-- +goose Up
-- Uploads through the web file manager in progress (docs/WINGS.md#files-and-sftp).
-- The data goes to a staging file in the server's directory; what's been
-- received is that file's size, so an upload resumes after a Wings restart.
CREATE TABLE file_uploads (
    id         TEXT PRIMARY KEY,
    server_id  TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL,
    path       TEXT NOT NULL, -- where it goes, relative to the server's directory
    size       INTEGER NOT NULL,
    created_at INTEGER NOT NULL, -- unix ms
    touched_at INTEGER NOT NULL -- unix ms, the last chunk; idle uploads expire
) STRICT;

CREATE INDEX file_uploads_server ON file_uploads (server_id);

-- +goose Down
DROP TABLE file_uploads;
