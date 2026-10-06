-- +goose Up
-- The audit log (docs/PANEL.md#auth): sign-ins and their failures, changes
-- to how accounts sign in, and every change to an org. Kept a year. Users
-- see their account's rows; admins and owners see their org's.
CREATE TABLE audit_log (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    -- The org it happened in (account events have none).
    org_id     uuid        REFERENCES orgs (id) ON DELETE CASCADE,
    -- The account it's about (a failed sign-in to an unknown address has none).
    user_id    uuid        REFERENCES users (id) ON DELETE CASCADE,
    actor      text        NOT NULL CHECK (actor IN ('user', 'staff', 'system')),
    actor_id   uuid,
    action     text        NOT NULL,
    target     text        NOT NULL DEFAULT '',
    ip         inet,
    user_agent text        NOT NULL DEFAULT '',
    metadata   jsonb       NOT NULL DEFAULT '{}',
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_org ON audit_log (org_id, id DESC) WHERE org_id IS NOT NULL;
CREATE INDEX audit_log_user ON audit_log (user_id, id DESC) WHERE user_id IS NOT NULL;
CREATE INDEX audit_log_at ON audit_log (at);

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT ON audit_log TO raptor_app;
CREATE POLICY audit_read ON audit_log FOR SELECT TO raptor_app
    USING (org_id IS NOT NULL AND raptor_org_role(org_id) IN ('admin', 'owner'));
-- Requests can add their own org's events, never rewrite any.
CREATE POLICY audit_write ON audit_log FOR INSERT TO raptor_app
    WITH CHECK (org_id IS NOT NULL AND raptor_org_role(org_id) IS NOT NULL AND actor_id = raptor_uid());

-- +goose Down
DROP TABLE audit_log;
