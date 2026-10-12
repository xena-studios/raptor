-- +goose Up
-- The egg library (docs/PANEL.md#eggs): an org's own eggs (imported, made
-- from scratch, or customized copies) and Raptor's official ones (org_id
-- NULL, managed by staff), each with every version kept. Replaces
-- org_eggs. The file is the egg: what the editor shows is read from it,
-- and edits are written back into it.
CREATE TABLE eggs (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        REFERENCES orgs (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    summary     text        NOT NULL DEFAULT '',
    category    text        NOT NULL DEFAULT 'other',
    origin      text        NOT NULL CHECK (origin IN ('scratch', 'import', 'fork')),
    -- Only published eggs can be deployed; archived ones are hidden.
    status      text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    version     integer     NOT NULL DEFAULT 1,
    egg         bytea       NOT NULL,
    sha256      text        NOT NULL,
    -- Where an imported egg was fetched from; empty for a file.
    source_url  text        NOT NULL DEFAULT '',
    -- What a customized copy was made from (a built-in egg's ID or a
    -- library egg's), and its name then.
    parent_id   text        NOT NULL DEFAULT '',
    parent_name text        NOT NULL DEFAULT '',
    created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX eggs_org ON eggs (org_id, name);
CREATE INDEX eggs_official ON eggs (name) WHERE org_id IS NULL;

CREATE TABLE egg_versions (
    egg_id     uuid        NOT NULL REFERENCES eggs (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    egg        bytea       NOT NULL,
    sha256     text        NOT NULL,
    note       text        NOT NULL DEFAULT '',
    created_by text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (egg_id, version)
);

INSERT INTO eggs (id, org_id, name, origin, status, egg, sha256, source_url, created_by, created_at, updated_at)
SELECT id, org_id, name, 'import', 'published', egg, sha256, source_url, imported_by, imported_at, imported_at
FROM org_eggs;
INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by, created_at)
SELECT o.id, 1, o.egg, o.sha256, 'Imported', coalesce(u.email, ''), o.imported_at
FROM org_eggs o LEFT JOIN users u ON u.id = o.imported_by;
DROP TABLE org_eggs;

-- Members see their org's eggs and Raptor's published official ones;
-- admins and owners change their org's. Official eggs are changed by
-- staff, through the Panel's own connection.
ALTER TABLE eggs ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE, DELETE ON eggs TO raptor_app;
CREATE POLICY eggs_read ON eggs FOR SELECT TO raptor_app
    USING ((org_id IS NULL AND status = 'published') OR raptor_org_role(org_id) IS NOT NULL);
CREATE POLICY eggs_insert ON eggs FOR INSERT TO raptor_app
    WITH CHECK (org_id IS NOT NULL AND raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY eggs_update ON eggs FOR UPDATE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'))
    WITH CHECK (org_id IS NOT NULL AND raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY eggs_delete ON eggs FOR DELETE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));

ALTER TABLE egg_versions ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT ON egg_versions TO raptor_app;
CREATE POLICY egg_versions_read ON egg_versions FOR SELECT TO raptor_app
    USING (EXISTS (SELECT 1 FROM eggs e WHERE e.id = egg_id));
CREATE POLICY egg_versions_insert ON egg_versions FOR INSERT TO raptor_app
    WITH CHECK (EXISTS (SELECT 1 FROM eggs e WHERE e.id = egg_id AND e.org_id IS NOT NULL
        AND raptor_org_role(e.org_id) IN ('admin', 'owner')));

-- +goose Down
CREATE TABLE org_eggs (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    egg         bytea       NOT NULL,
    sha256      text        NOT NULL,
    source_url  text        NOT NULL DEFAULT '',
    imported_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, sha256)
);
CREATE INDEX org_eggs_org ON org_eggs (org_id, name);
INSERT INTO org_eggs (id, org_id, name, egg, sha256, source_url, imported_by, imported_at)
SELECT DISTINCT ON (org_id, sha256) id, org_id, name, egg, sha256, source_url, created_by, created_at
FROM eggs WHERE org_id IS NOT NULL ORDER BY org_id, sha256, created_at;
ALTER TABLE org_eggs ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, DELETE ON org_eggs TO raptor_app;
CREATE POLICY org_eggs_read ON org_eggs FOR SELECT TO raptor_app USING (raptor_org_role(org_id) IS NOT NULL);
CREATE POLICY org_eggs_insert ON org_eggs FOR INSERT TO raptor_app WITH CHECK (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY org_eggs_delete ON org_eggs FOR DELETE TO raptor_app USING (raptor_org_role(org_id) IN ('admin', 'owner'));
DROP TABLE egg_versions;
DROP TABLE eggs;
