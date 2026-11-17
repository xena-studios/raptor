-- +goose Up
-- The public demo, Phase 1 (#449, docs/PANEL.md#the-public-demo). Only
-- additive: the Panel being replaced runs against this schema during a
-- deploy, and ignores all of it.
--
-- orgs.demo marks the demo org. Only staff set it (`panel staff demo on`,
-- which connects as the database's owner); no query the web app's role
-- runs changes it. demo_reset_at is when the hourly reset last ran.
ALTER TABLE orgs ADD COLUMN demo boolean NOT NULL DEFAULT false;
ALTER TABLE orgs ADD COLUMN demo_reset_at timestamptz;

-- A guest: a throwaway account made by AuthService.StartDemo, with no
-- email of its own (users.email is guest-<random>@demo.invalid, which is
-- never sent to) and no sign-in method. It's deleted, with its session and
-- membership, once guest_until passes, and at every hourly reset.
ALTER TABLE users ADD COLUMN guest_until timestamptz;
CREATE INDEX users_guests ON users (guest_until) WHERE guest_until IS NOT NULL;

-- The demo org's showcase servers: what each guest is granted on them, and
-- the backup that's each one's known-good state (restored by the hourly
-- reset once the demo node trusts a Panel-held demo key, Phase 2; DECISIONS
-- #375). Read and written by the Panel only, never as a user.
CREATE TABLE demo_servers (
    org_id            uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    node_id           uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    server_id         text        NOT NULL,
    permissions       text[]      NOT NULL CHECK (cardinality(permissions) > 0),
    known_good_backup text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, server_id)
);
CREATE INDEX demo_servers_org ON demo_servers (org_id);

-- +goose Down
DROP TABLE demo_servers;
DROP INDEX users_guests;
ALTER TABLE users DROP COLUMN guest_until;
ALTER TABLE orgs DROP COLUMN demo_reset_at;
ALTER TABLE orgs DROP COLUMN demo;
