-- name: UpsertMetric :exec
INSERT INTO metrics (server_id, resolution, at, samples, cpu_avg, cpu_max, memory_avg, memory_max, rx_bytes, tx_bytes, disk_bytes, players_avg, players_max)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, resolution, at) DO UPDATE SET
    samples = excluded.samples, cpu_avg = excluded.cpu_avg, cpu_max = excluded.cpu_max,
    memory_avg = excluded.memory_avg, memory_max = excluded.memory_max, rx_bytes = excluded.rx_bytes,
    tx_bytes = excluded.tx_bytes, disk_bytes = excluded.disk_bytes, players_avg = excluded.players_avg,
    players_max = excluded.players_max;

-- name: ListMetrics :many
SELECT * FROM metrics WHERE server_id = ? AND resolution = ? AND at >= ? ORDER BY at;

-- name: MetricsBefore :many
-- Minute rows old enough to be rolled up.
SELECT * FROM metrics WHERE resolution = 60 AND at < ? ORDER BY server_id, at;

-- name: DeleteMetricsBefore :execrows
DELETE FROM metrics WHERE resolution = ? AND at < ?;
