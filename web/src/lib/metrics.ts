import { useQuery } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

// What Wings' server.metrics returns (internal/wings/actions/metrics.go).
export type MetricPoint = {
  at: number;
  resolution: number;
  cpu: number;
  cpu_max: number;
  memory: number;
  memory_max: number;
  rx: number;
  tx: number;
  disk: number;
  players?: number;
  players_max?: number;
  running: boolean;
};

export type Metrics = {
  range: MetricRange;
  latest: MetricPoint;
  points: MetricPoint[];
  limits: { memory_bytes: number; disk_bytes: number; cpu_percent: number };
};

export type MetricRange = "1h" | "6h" | "24h" | "7d";

export const metricRanges: { value: MetricRange; label: string; ms: number }[] = [
  { value: "1h", label: "1 hour", ms: 3600_000 },
  { value: "6h", label: "6 hours", ms: 6 * 3600_000 },
  { value: "24h", label: "24 hours", ms: 24 * 3600_000 },
  { value: "7d", label: "7 days", ms: 7 * 24 * 3600_000 },
];

// useMetrics is a server's resources, refreshed every 10 seconds (Wings
// samples that often) for the last hour, every minute for longer ranges.
export function useMetrics(nodeId: string, serverId: string, range: MetricRange) {
  return useQuery({
    queryKey: ["metrics", nodeId, serverId, range],
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "server.metrics",
        paramsJson: JSON.stringify({ range }),
      });
      return JSON.parse(res.resultJson) as Metrics;
    },
    refetchInterval: range === "1h" ? 10_000 : 60_000,
    placeholderData: (prev) => prev,
  });
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${Math.round(n)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n;
  let u = -1;
  do {
    v /= 1024;
    u++;
  } while (v >= 1024 && u < units.length - 1);
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[u]}`;
}

export const formatRate = (n: number) => `${formatBytes(n)}/s`;
export const formatPercent = (n: number) => `${n < 10 ? n.toFixed(1) : Math.round(n)}%`;
