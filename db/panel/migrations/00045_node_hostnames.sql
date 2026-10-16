-- +goose Up
-- The node domain is only for nodes (docs/ARCHITECTURE.md#node-dns): a
-- node's short ID is the last part of its ID, and its hostname is
-- <short ID>.raptornodes.net, with no n-. Player subdomains are gone.

-- Records the Panel wrote that should go: names relative to the node
-- domain, deleted by the Panel (nodes.Addresses) and then forgotten.
CREATE TABLE dns_retired (
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('A', 'AAAA', 'CNAME', 'SRV')),
    PRIMARY KEY (name, type)
);

-- Nodes' old hostnames.
INSERT INTO dns_retired (name, type)
SELECT 'n-' || short_id, 'A' FROM nodes WHERE dns_ipv4 IS NOT NULL
UNION
SELECT 'n-' || short_id, 'AAAA' FROM nodes WHERE dns_ipv6 IS NOT NULL;

-- Player subdomains: the CNAME, and the SRV record Minecraft asks for.
INSERT INTO dns_retired (name, type)
SELECT name, 'CNAME' FROM subdomains WHERE dns_target <> ''
UNION
SELECT '_minecraft._tcp.' || name, 'SRV' FROM subdomains WHERE dns_port <> 0
ON CONFLICT DO NOTHING;

DROP TABLE subdomain_bans;
DROP TABLE subdomains;

-- The short ID follows the ID, so it can't be anything else. UUIDv7's last
-- 12 hex digits are random; rows are never deleted, so it's never reused.
-- The new hostnames have no records yet: a node gets them when it next
-- connects.
ALTER TABLE nodes DROP COLUMN short_id;
ALTER TABLE nodes ADD COLUMN short_id text NOT NULL GENERATED ALWAYS AS (split_part(id::text, '-', 5)) STORED;
ALTER TABLE nodes ADD CONSTRAINT nodes_short_id_key UNIQUE (short_id);
UPDATE nodes SET dns_ipv4 = NULL, dns_ipv6 = NULL;

-- +goose Down
-- Subdomains don't come back: their tables were dropped with the feature.
ALTER TABLE nodes DROP COLUMN short_id;
ALTER TABLE nodes ADD COLUMN short_id text;
UPDATE nodes SET short_id = substr(md5(id::text), 1, 8);
ALTER TABLE nodes ALTER COLUMN short_id SET NOT NULL;
ALTER TABLE nodes ADD CONSTRAINT nodes_short_id_key UNIQUE (short_id);
DROP TABLE dns_retired;
