-- +goose Up
-- Community eggs (docs/PANEL.md#community-eggs): an org shares a published
-- egg with everyone on Raptor; anyone can report one; staff can take one
-- out of the community.
ALTER TABLE eggs
    ADD COLUMN shared_at      timestamptz,
    ADD COLUMN removed_at     timestamptz,
    ADD COLUMN removed_reason text NOT NULL DEFAULT '',
    ADD COLUMN removed_by     text NOT NULL DEFAULT '';
CREATE INDEX eggs_community ON eggs (shared_at DESC) WHERE shared_at IS NOT NULL AND removed_at IS NULL;
CREATE INDEX eggs_parent ON eggs (parent_id) WHERE parent_id <> '';

-- Orgs change their eggs, but never whether staff removed one.
REVOKE UPDATE ON eggs FROM raptor_app;
GRANT UPDATE (name, summary, category, status, version, egg, sha256, updated_at, shared_at) ON eggs TO raptor_app;

-- Everyone sees what's shared and not removed. A removed egg can't be
-- shared again by its org.
DROP POLICY eggs_read ON eggs;
CREATE POLICY eggs_read ON eggs FOR SELECT TO raptor_app
    USING ((org_id IS NULL AND status = 'published')
        OR (shared_at IS NOT NULL AND removed_at IS NULL AND status = 'published')
        OR raptor_org_role(org_id) IS NOT NULL);
DROP POLICY eggs_update ON eggs;
CREATE POLICY eggs_update ON eggs FOR UPDATE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'))
    WITH CHECK (org_id IS NOT NULL AND raptor_org_role(org_id) IN ('admin', 'owner')
        AND (shared_at IS NULL OR (removed_at IS NULL AND status = 'published')));

CREATE TABLE egg_reports (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    egg_id      uuid        NOT NULL REFERENCES eggs (id) ON DELETE CASCADE,
    reporter_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- malware, broken, spam, or other.
    reason      text        NOT NULL CHECK (reason IN ('malware', 'broken', 'spam', 'other')),
    details     text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by text        NOT NULL DEFAULT '',
    -- removed (the egg was taken out) or dismissed.
    resolution  text        NOT NULL DEFAULT ''
);
CREATE INDEX egg_reports_open ON egg_reports (created_at) WHERE resolved_at IS NULL;
CREATE UNIQUE INDEX egg_reports_once ON egg_reports (egg_id, reporter_id) WHERE resolved_at IS NULL;
-- Reports are written and read by the Panel's own connection.

-- +goose Down
DROP TABLE egg_reports;
DROP POLICY eggs_update ON eggs;
CREATE POLICY eggs_update ON eggs FOR UPDATE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'))
    WITH CHECK (org_id IS NOT NULL AND raptor_org_role(org_id) IN ('admin', 'owner'));
DROP POLICY eggs_read ON eggs;
CREATE POLICY eggs_read ON eggs FOR SELECT TO raptor_app
    USING ((org_id IS NULL AND status = 'published') OR raptor_org_role(org_id) IS NOT NULL);
REVOKE UPDATE ON eggs FROM raptor_app;
GRANT UPDATE ON eggs TO raptor_app;
DROP INDEX eggs_parent;
DROP INDEX eggs_community;
ALTER TABLE eggs DROP COLUMN removed_by, DROP COLUMN removed_reason, DROP COLUMN removed_at, DROP COLUMN shared_at;
