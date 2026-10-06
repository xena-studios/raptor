-- +goose Up
-- Row-level security by org (docs/PANEL.md#permissions). Requests made for
-- a user run as raptor_app with raptor.user_id set for the transaction
-- (orgs.Service.asUser); the policies then only show that user's orgs, so
-- a missing check in the code can't leak another org's rows. The Panel's
-- own work (node connections, the mirror, rollouts, signing in) runs as the
-- tables' owner, which RLS doesn't apply to.

-- Roles are per cluster; tests migrate many databases at once.
-- +goose StatementBegin
DO $$
BEGIN
    CREATE ROLE raptor_app NOLOGIN;
EXCEPTION WHEN duplicate_object OR unique_violation THEN
    NULL;
END
$$;
-- +goose StatementEnd
GRANT raptor_app TO CURRENT_USER;

-- The user the transaction is for (NULL: nobody, so nothing is visible).
CREATE FUNCTION raptor_uid() RETURNS uuid
LANGUAGE sql STABLE
AS $$ SELECT NULLIF(current_setting('raptor.user_id', true), '')::uuid $$;

-- The user's role in an org (NULL if not a member). SECURITY DEFINER: it
-- reads org_members as the owner, so policies on org_members can use it
-- without recursing.
CREATE FUNCTION raptor_org_role(org uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public
AS $$ SELECT role FROM org_members WHERE org_id = org AND user_id = raptor_uid() $$;

-- Whether another user shares an org with the user.
CREATE FUNCTION raptor_shares_org(other uuid) RETURNS bool
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public
AS $$
    SELECT EXISTS (
        SELECT 1 FROM org_members a JOIN org_members b ON a.org_id = b.org_id
        WHERE a.user_id = raptor_uid() AND b.user_id = other
    )
$$;

-- Whether the user is in the org that owns a node.
CREATE FUNCTION raptor_node_visible(node uuid) RETURNS bool
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public
AS $$
    SELECT EXISTS (
        SELECT 1 FROM nodes n JOIN org_members m ON m.org_id = n.org_id
        WHERE n.id = node AND m.user_id = raptor_uid()
    )
$$;

REVOKE ALL ON FUNCTION raptor_org_role(uuid), raptor_shares_org(uuid), raptor_node_visible(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION raptor_uid(), raptor_org_role(uuid), raptor_shares_org(uuid), raptor_node_visible(uuid) TO raptor_app;

ALTER TABLE orgs ENABLE ROW LEVEL SECURITY;
GRANT SELECT, UPDATE ON orgs TO raptor_app;
CREATE POLICY orgs_read ON orgs FOR SELECT TO raptor_app USING (raptor_org_role(id) IS NOT NULL);
CREATE POLICY orgs_write ON orgs FOR UPDATE TO raptor_app USING (raptor_org_role(id) IN ('admin', 'owner'));

ALTER TABLE org_members ENABLE ROW LEVEL SECURITY;
GRANT SELECT, UPDATE, DELETE ON org_members TO raptor_app;
CREATE POLICY members_read ON org_members FOR SELECT TO raptor_app USING (raptor_org_role(org_id) IS NOT NULL);
-- Only owners change roles (and lock rows to check the last owner).
CREATE POLICY members_update ON org_members FOR UPDATE TO raptor_app USING (raptor_org_role(org_id) = 'owner');
-- Owners remove anyone, admins remove members, and anyone can leave.
CREATE POLICY members_delete ON org_members FOR DELETE TO raptor_app USING (
    raptor_org_role(org_id) = 'owner'
    OR (raptor_org_role(org_id) = 'admin' AND role = 'member')
    OR user_id = raptor_uid()
);

ALTER TABLE org_invitations ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE ON org_invitations TO raptor_app;
CREATE POLICY invitations_admin ON org_invitations FOR ALL TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'))
    WITH CHECK (raptor_org_role(org_id) IN ('admin', 'owner'));

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON users TO raptor_app;
CREATE POLICY users_read ON users FOR SELECT TO raptor_app USING (id = raptor_uid() OR raptor_shares_org(id));

ALTER TABLE nodes ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON nodes TO raptor_app;
CREATE POLICY nodes_read ON nodes FOR SELECT TO raptor_app USING (raptor_org_role(org_id) IS NOT NULL);

ALTER TABLE join_tokens ENABLE ROW LEVEL SECURITY;

ALTER TABLE m_servers ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON m_servers TO raptor_app;
CREATE POLICY m_servers_read ON m_servers FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

ALTER TABLE m_schedules ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON m_schedules TO raptor_app;
CREATE POLICY m_schedules_read ON m_schedules FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

ALTER TABLE m_backups ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON m_backups TO raptor_app;
CREATE POLICY m_backups_read ON m_backups FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

-- +goose Down
DROP POLICY m_backups_read ON m_backups;
ALTER TABLE m_backups DISABLE ROW LEVEL SECURITY;
DROP POLICY m_schedules_read ON m_schedules;
ALTER TABLE m_schedules DISABLE ROW LEVEL SECURITY;
DROP POLICY m_servers_read ON m_servers;
ALTER TABLE m_servers DISABLE ROW LEVEL SECURITY;
ALTER TABLE join_tokens DISABLE ROW LEVEL SECURITY;
DROP POLICY nodes_read ON nodes;
ALTER TABLE nodes DISABLE ROW LEVEL SECURITY;
DROP POLICY users_read ON users;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
DROP POLICY invitations_admin ON org_invitations;
ALTER TABLE org_invitations DISABLE ROW LEVEL SECURITY;
DROP POLICY members_delete ON org_members;
DROP POLICY members_update ON org_members;
DROP POLICY members_read ON org_members;
ALTER TABLE org_members DISABLE ROW LEVEL SECURITY;
DROP POLICY orgs_write ON orgs;
DROP POLICY orgs_read ON orgs;
ALTER TABLE orgs DISABLE ROW LEVEL SECURITY;
REVOKE ALL ON orgs, org_members, org_invitations, users, nodes, m_servers, m_schedules, m_backups FROM raptor_app;
DROP FUNCTION raptor_node_visible(uuid);
DROP FUNCTION raptor_shares_org(uuid);
DROP FUNCTION raptor_org_role(uuid);
DROP FUNCTION raptor_uid();
