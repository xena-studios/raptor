-- +goose Up
-- Player subdomains on the node domain (docs/PANEL.md#subdomains):
-- smp.raptornodes.net for one server. Names are one namespace across every
-- org, so they're claimed and released by the Panel's own connection after
-- it checks the user; orgs read their own.
CREATE TABLE subdomains (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    node_id     uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    server_id   text        NOT NULL,
    -- 3 to 32 of a-z, 0-9, and inner hyphens. Reserved names and n- (node
    -- hostnames) are refused by the Panel.
    name        text        NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$'),
    created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- Released (the subdomain was removed, or its server or node is gone):
    -- its records are deleted, and the name stays the org's for 30 days, so
    -- players' saved addresses don't reach someone else's server at once.
    released_at timestamptz,
    -- What DNS has, as the Panel last wrote it: the target (a hostname or
    -- an IP; '' for nothing), and the port in its SRV record (0 for none).
    dns_target  text        NOT NULL DEFAULT '',
    dns_port    integer     NOT NULL DEFAULT 0,
    dns_error   text        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX subdomains_server ON subdomains (node_id, server_id) WHERE released_at IS NULL;
CREATE INDEX subdomains_org ON subdomains (org_id) WHERE released_at IS NULL;

ALTER TABLE subdomains ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON subdomains TO raptor_app;
CREATE POLICY subdomains_read ON subdomains FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IS NOT NULL);

-- Names staff took away for abuse, and names staff hold back: never
-- claimable.
CREATE TABLE subdomain_bans (
    name       text        PRIMARY KEY,
    reason     text        NOT NULL,
    staff_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE subdomain_bans;
DROP TABLE subdomains;
