-- +goose Up
-- A server's shown address (docs/PANEL.md#node-addresses): what players
-- connect to instead of its node's hostname, for a server behind a proxy or
-- the owner's own domain. No row shows the hostname, hidden. Same format as
-- nodes.display_address, which it replaces: the shown address is a server's,
-- and the node's pages show the node's own address. Kept apart from the
-- mirror, which is the node's.
CREATE TABLE server_addresses (
    node_id         uuid        NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    server_id       text        NOT NULL,
    display_address text        NOT NULL CHECK (display_address <> '' AND length(display_address) <= 260),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, server_id)
);

ALTER TABLE server_addresses ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON server_addresses TO raptor_app;
CREATE POLICY server_addresses_read ON server_addresses FOR SELECT TO raptor_app
    USING (raptor_node_visible(node_id));

-- Each node's shown address moves to its servers.
INSERT INTO server_addresses (node_id, server_id, display_address)
SELECT s.node_id, s.server_id, n.display_address
FROM m_servers s JOIN nodes n ON n.id = s.node_id
WHERE n.display_address <> '' AND n.deleted_at IS NULL;
UPDATE nodes SET display_address = '' WHERE display_address <> '';

-- +goose Down
DROP TABLE server_addresses;
