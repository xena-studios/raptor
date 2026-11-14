-- +goose Up
-- Eggs' icons (docs/PANEL.md#images): the picture in the egg's own file
-- (Pelican's "image", or a catalog entry's icon.png), or one an admin
-- uploaded, which the file's never replaces.
ALTER TABLE eggs ADD COLUMN icon text REFERENCES images (id) ON DELETE SET NULL;
ALTER TABLE eggs ADD COLUMN icon_source text NOT NULL DEFAULT '' CHECK (icon_source IN ('', 'file', 'upload'));
CREATE INDEX eggs_icon ON eggs (icon) WHERE icon IS NOT NULL;
GRANT UPDATE (icon, icon_source) ON eggs TO raptor_app;

-- +goose Down
REVOKE UPDATE (icon, icon_source) ON eggs FROM raptor_app;
DROP INDEX eggs_icon;
ALTER TABLE eggs DROP COLUMN icon_source;
ALTER TABLE eggs DROP COLUMN icon;
