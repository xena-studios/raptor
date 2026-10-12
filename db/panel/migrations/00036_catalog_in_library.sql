-- +goose Up
-- Raptor's catalog moves into the official library (docs/PANEL.md#eggs):
-- the Panel copies the eggs in eggs/ into it on start, as official eggs
-- staff can edit like any other. catalog_id is the catalog's ID
-- ("minecraft/paper"), which servers made before name the egg by.
-- certified: the file is the one the conformance suite tests; editing it
-- clears this.
ALTER TABLE eggs
    ADD COLUMN catalog_id text,
    ADD COLUMN certified  boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX eggs_catalog_id ON eggs (catalog_id) WHERE catalog_id IS NOT NULL;
ALTER TABLE eggs DROP CONSTRAINT eggs_origin_check;
ALTER TABLE eggs ADD CONSTRAINT eggs_origin_check CHECK (origin IN ('scratch', 'import', 'fork', 'catalog'));

-- +goose Down
DELETE FROM eggs WHERE origin = 'catalog';
ALTER TABLE eggs DROP CONSTRAINT eggs_origin_check;
ALTER TABLE eggs ADD CONSTRAINT eggs_origin_check CHECK (origin IN ('scratch', 'import', 'fork'));
DROP INDEX eggs_catalog_id;
ALTER TABLE eggs DROP COLUMN certified, DROP COLUMN catalog_id;
