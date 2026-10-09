-- +goose Up
-- Eggs an org imported from a URL or a file (docs/PANEL.md#imported-eggs),
-- beside the built-in catalog. The file is kept as it was reviewed.
CREATE TABLE org_eggs (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    egg         bytea       NOT NULL,
    sha256      text        NOT NULL,
    -- Where it was fetched from; empty for an uploaded file.
    source_url  text        NOT NULL DEFAULT '',
    imported_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, sha256)
);
CREATE INDEX org_eggs_org ON org_eggs (org_id, name);

ALTER TABLE org_eggs ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, DELETE ON org_eggs TO raptor_app;
CREATE POLICY org_eggs_read ON org_eggs FOR SELECT TO raptor_app USING (raptor_org_role(org_id) IS NOT NULL);
CREATE POLICY org_eggs_insert ON org_eggs FOR INSERT TO raptor_app WITH CHECK (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY org_eggs_delete ON org_eggs FOR DELETE TO raptor_app USING (raptor_org_role(org_id) IN ('admin', 'owner'));

-- +goose Down
DROP TABLE org_eggs;
