-- +goose Up
-- Whether a node is connected, for the people in its org.
ALTER TABLE node_connections ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON node_connections TO raptor_app;
CREATE POLICY node_connections_read ON node_connections FOR SELECT TO raptor_app USING (raptor_node_visible(node_id));

-- +goose Down
DROP POLICY node_connections_read ON node_connections;
ALTER TABLE node_connections DISABLE ROW LEVEL SECURITY;
REVOKE ALL ON node_connections FROM raptor_app;
