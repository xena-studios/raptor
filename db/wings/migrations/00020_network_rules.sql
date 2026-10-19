-- +goose Up
-- Each server's fixed address on each of the owner's networks it's on, so
-- rules between networks can name one server (docs/NETWORKS.md).
CREATE TABLE server_addresses (
    server_id  TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES networks (id) ON DELETE CASCADE,
    ip         TEXT NOT NULL,
    PRIMARY KEY (server_id, network_id),
    UNIQUE (network_id, ip)
);

-- +goose Down
DROP TABLE server_addresses;
