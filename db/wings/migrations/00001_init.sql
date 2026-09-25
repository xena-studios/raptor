-- +goose Up
CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE kv;
