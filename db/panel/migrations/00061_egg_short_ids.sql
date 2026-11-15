-- +goose Up
-- Egg pages' URLs carry the egg's short ID, its UUID's last part, like
-- orgs', nodes', and servers' (web/src/lib/short-id.ts); the Panel finds
-- the egg by it among those the library can see.
CREATE INDEX eggs_short_id ON eggs (split_part(id::text, '-', 5));

-- +goose Down
DROP INDEX eggs_short_id;
