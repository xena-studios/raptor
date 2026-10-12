-- +goose Up
-- The machine's resource history (docs/WINGS.md#local-metrics), kept like
-- servers': one row per minute for a day, then one per 15 minutes for a week.
CREATE TABLE host_metrics (
    resolution  INTEGER NOT NULL, -- seconds: 60 or 900
    at          INTEGER NOT NULL, -- unix ms, the bucket's start
    samples     INTEGER NOT NULL,
    cpu_avg     REAL NOT NULL,    -- percent of the whole machine
    cpu_max     REAL NOT NULL,
    memory_avg  INTEGER NOT NULL, -- bytes in use (total less available)
    memory_max  INTEGER NOT NULL,
    rx_bytes    INTEGER NOT NULL, -- traffic on physical interfaces in the bucket
    tx_bytes    INTEGER NOT NULL,
    disk_bytes  INTEGER NOT NULL, -- used on the servers' filesystem, at the end
    load_avg    REAL NOT NULL,    -- the 1-minute load average
    PRIMARY KEY (resolution, at)
) STRICT;

-- +goose Down
DROP TABLE host_metrics;
