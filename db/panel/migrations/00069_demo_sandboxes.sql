-- +goose Up
-- The public demo, Phases 2 and 3 (#449, docs/PANEL.md#sandboxes): guests'
-- sandbox servers on the demo node. Only additive: the Panel being
-- replaced runs against this schema during a deploy, and ignores all of it.
-- Read and written by the Panel only, never as a user.

-- A demo node: one whose owner ran `raptor demo on` with this node's demo
-- key (`panel staff demo node` adds the row and shows the key). The
-- settings are the Panel's side of what Wings enforces on the node.
CREATE TABLE demo_nodes (
    node_id       uuid        PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    org_id        uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    max_sandboxes integer     NOT NULL DEFAULT 4 CHECK (max_sandboxes BETWEEN 0 AND 32),
    port_min      integer     NOT NULL DEFAULT 30000,
    port_max      integer     NOT NULL DEFAULT 30099,
    -- How many of the most-picked eggs get a warm template each night.
    warm_eggs     integer     NOT NULL DEFAULT 6 CHECK (warm_eggs BETWEEN 0 AND 32),
    -- The last night the templates were built (UTC).
    warmed_on     date,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (port_min BETWEEN 1024 AND 65535 AND port_max BETWEEN port_min AND 65535)
);

-- A guest's sandbox, from the queue to its deletion, or a warm template
-- being built (warm, no user). Kept after it ends, without the guest, to
-- count which eggs guests pick (the warm pool's choice, and analytics).
-- ip_hash is a keyed hash of the guest's address, for one sandbox per
-- address, never the address itself.
CREATE TABLE demo_sandboxes (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    node_id     uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    user_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    ip_hash     bytea       NOT NULL DEFAULT '',
    egg_id      uuid        NOT NULL,
    library_id  text        NOT NULL,
    egg_name    text        NOT NULL,
    accept_eula boolean     NOT NULL DEFAULT false,
    warm        boolean     NOT NULL DEFAULT false,
    state       text        NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'creating', 'installing', 'ready', 'gone', 'failed')),
    server_id   text        NOT NULL DEFAULT '',
    port        integer     NOT NULL DEFAULT 0,
    error       text        NOT NULL DEFAULT '',
    queued_at   timestamptz NOT NULL DEFAULT now(),
    created_at  timestamptz,
    ready_at    timestamptz,
    deadline    timestamptz,
    extended    boolean     NOT NULL DEFAULT false,
    ended_at    timestamptz
);
-- One live sandbox per guest, and per address.
CREATE UNIQUE INDEX demo_sandboxes_user ON demo_sandboxes (user_id)
    WHERE user_id IS NOT NULL AND state IN ('queued', 'creating', 'installing', 'ready');
CREATE UNIQUE INDEX demo_sandboxes_ip ON demo_sandboxes (ip_hash)
    WHERE NOT warm AND state IN ('queued', 'creating', 'installing', 'ready');
CREATE INDEX demo_sandboxes_live ON demo_sandboxes (node_id, queued_at)
    WHERE state IN ('queued', 'creating', 'installing', 'ready');
CREATE INDEX demo_sandboxes_picked ON demo_sandboxes (library_id, queued_at) WHERE NOT warm;

-- +goose Down
DROP TABLE demo_sandboxes;
DROP TABLE demo_nodes;
