-- +goose Up
-- Raptor Backup Storage (docs/PANEL.md#raptor-backup-storage): a node's B2
-- key, and each org's daily usage. Only the Panel's own pool reads and
-- writes these; nothing is granted to raptor_app.

-- One row per time a node had it turned on. The key's secret went to the
-- node and isn't kept. No foreign key to nodes: a removed node's data is
-- still deleted 30 days later.
CREATE TABLE backup_storage (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL,
    node_id     uuid        NOT NULL,
    key_id      text        NOT NULL,
    enabled_at  timestamptz NOT NULL DEFAULT now(),
    enabled_by  uuid,
    -- Turned off (or the node removed): the key is deleted then, the data
    -- 30 days later (purged_at).
    disabled_at timestamptz,
    purged_at   timestamptz
);
CREATE UNIQUE INDEX backup_storage_active ON backup_storage (node_id) WHERE disabled_at IS NULL;
CREATE INDEX backup_storage_org ON backup_storage (org_id);

-- What an org stored, measured once a day: for showing, and for billing.
CREATE TABLE backup_storage_usage (
    org_id      uuid        NOT NULL,
    day         date        NOT NULL,
    bytes       bigint      NOT NULL,
    measured_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, day)
);

ALTER TABLE backup_storage ENABLE ROW LEVEL SECURITY;
ALTER TABLE backup_storage_usage ENABLE ROW LEVEL SECURITY;

-- +goose Down
DROP TABLE backup_storage_usage;
DROP TABLE backup_storage;
