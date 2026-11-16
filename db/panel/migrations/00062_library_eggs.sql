-- +goose Up
-- Raptor's egg library, seeded into the official library on every start
-- (internal/panel/catalog, docs/EGGS.md#egg-library). internal: one of
-- Raptor's own tools (the test server), which orgs' libraries don't list.
-- library_sum: what the seed last wrote (the egg file, its icon.png, and
-- the library's settings), so a start with nothing changed writes nothing.
ALTER TABLE eggs ADD COLUMN internal boolean NOT NULL DEFAULT false;
ALTER TABLE eggs ADD COLUMN library_sum text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE eggs DROP COLUMN library_sum;
ALTER TABLE eggs DROP COLUMN internal;
