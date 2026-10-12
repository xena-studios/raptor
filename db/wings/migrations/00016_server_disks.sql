-- +goose Up
-- Which disk a server's files are on (docs/WINGS.md#data-disks): '' is the
-- main server data volume, otherwise a data disk mounted at
-- <state dir>/disks/<disk_id>. Chosen when the server is created.
ALTER TABLE servers ADD COLUMN disk_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE servers DROP COLUMN disk_id;
