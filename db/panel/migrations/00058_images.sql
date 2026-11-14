-- +goose Up
-- Pictures people give the Panel: org logos and user avatars
-- (docs/PANEL.md#images). Each is stored once, as the PNG the Panel wrote
-- out of the upload (internal/panel/images), keyed by that PNG's SHA-256,
-- which is also its address: GET /api/images/<id>.png, public to anyone
-- who has the address, as the IDs are only shown to people who may see the
-- picture. Taking one down (staff) empties it but keeps the row, so the
-- same file can't just be uploaded again.
CREATE TABLE images (
    id            text        PRIMARY KEY CHECK (id ~ '^[0-9a-f]{64}$'),
    png           bytea       CHECK (length(png) <= 1048576),
    width         integer     NOT NULL CHECK (width BETWEEN 1 AND 256),
    height        integer     NOT NULL CHECK (height BETWEEN 1 AND 256),
    -- Refreshed when the same picture is uploaded again, so the daily
    -- cleanup of unused pictures can't remove one that's about to be used.
    created_at    timestamptz NOT NULL DEFAULT now(),
    taken_down_at timestamptz,
    CHECK ((png IS NULL) = (taken_down_at IS NOT NULL))
);

ALTER TABLE orgs ADD COLUMN logo text REFERENCES images (id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN avatar text REFERENCES images (id) ON DELETE SET NULL;
CREATE INDEX orgs_logo ON orgs (logo) WHERE logo IS NOT NULL;
CREATE INDEX users_avatar ON users (avatar) WHERE avatar IS NOT NULL;

-- Members' requests add pictures and can refresh them, but never change
-- one's pixels or bring back one that was taken down.
ALTER TABLE images ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT ON images TO raptor_app;
GRANT UPDATE (created_at) ON images TO raptor_app;
CREATE POLICY images_read ON images FOR SELECT TO raptor_app USING (true);
CREATE POLICY images_insert ON images FOR INSERT TO raptor_app WITH CHECK (taken_down_at IS NULL);
CREATE POLICY images_refresh ON images FOR UPDATE TO raptor_app
    USING (taken_down_at IS NULL) WITH CHECK (taken_down_at IS NULL);

-- +goose Down
DROP INDEX users_avatar;
DROP INDEX orgs_logo;
ALTER TABLE users DROP COLUMN avatar;
ALTER TABLE orgs DROP COLUMN logo;
DROP TABLE images;
