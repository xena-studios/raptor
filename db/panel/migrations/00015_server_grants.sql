-- +goose Up
-- What members (not admins or owners, who can do everything in their org)
-- may do on one server (docs/PANEL.md#permissions). Removing someone from
-- the org removes their grants.
CREATE TABLE server_grants (
    org_id      uuid        NOT NULL,
    user_id     uuid        NOT NULL,
    node_id     uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    server_id   text        NOT NULL,
    permissions text[]      NOT NULL CHECK (
        cardinality(permissions) > 0 AND permissions <@ ARRAY[
            'console.write', 'power', 'files.read', 'files.write', 'backups',
            'schedules', 'startup', 'reinstall', 'sftp'
        ]::text[]
    ),
    granted_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, server_id, user_id),
    FOREIGN KEY (org_id, user_id) REFERENCES org_members (org_id, user_id) ON DELETE CASCADE
);
CREATE INDEX server_grants_user ON server_grants (user_id);

ALTER TABLE server_grants ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE, DELETE ON server_grants TO raptor_app;
-- Members see their own grants; admins and owners see and manage the org's.
CREATE POLICY grants_read ON server_grants FOR SELECT TO raptor_app
    USING (user_id = raptor_uid() OR raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY grants_insert ON server_grants FOR INSERT TO raptor_app
    WITH CHECK (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY grants_update ON server_grants FOR UPDATE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'))
    WITH CHECK (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY grants_delete ON server_grants FOR DELETE TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));

-- +goose Down
DROP TABLE server_grants;
