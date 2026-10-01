-- +goose Up
-- Resource history per server (docs/WINGS.md#local-metrics): one row per
-- minute for a day, then one per 15 minutes for a week.
CREATE TABLE metrics (
    server_id   TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    resolution  INTEGER NOT NULL, -- seconds: 60 or 900
    at          INTEGER NOT NULL, -- unix ms, the bucket's start
    samples     INTEGER NOT NULL, -- how many samples, while the server ran
    cpu_avg     REAL NOT NULL,    -- percent of one core
    cpu_max     REAL NOT NULL,
    memory_avg  INTEGER NOT NULL, -- bytes
    memory_max  INTEGER NOT NULL,
    rx_bytes    INTEGER NOT NULL, -- network traffic in the bucket
    tx_bytes    INTEGER NOT NULL,
    disk_bytes  INTEGER NOT NULL, -- at the end of the bucket
    players_avg REAL,             -- NULL if the game isn't queried
    players_max INTEGER,
    PRIMARY KEY (server_id, resolution, at)
) STRICT;

-- +goose Down
DROP TABLE metrics;
