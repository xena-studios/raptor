-- +goose Up
-- Copies of nodes' backup keys (docs/PANEL.md#backup-keys), sealed with
-- PANEL_DATA_KEY and bound to their node. Kept after a node is removed:
-- recovering a dead node's backups is what they're for. Only the Panel's
-- own pool reads and writes them; nothing is granted to raptor_app.
CREATE TABLE backup_keys (
    node_id     uuid        PRIMARY KEY,
    org_id      uuid        NOT NULL,
    sealed      bytea       NOT NULL,
    fingerprint text        NOT NULL,
    stored_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backup_keys_org ON backup_keys (org_id);
ALTER TABLE backup_keys ENABLE ROW LEVEL SECURITY;

-- +goose Down
DROP TABLE backup_keys;
