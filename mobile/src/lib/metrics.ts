import { useQuery } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

// A server's resources and players, as the web app reads them
// (web/src/lib/metrics.ts): Wings' server.metrics, sampled every 10
// seconds; and, for admins, node.metrics, with what each server uses now.

export type MetricPoint = {
  at: number;
  cpu: number;
  memory: number;
  rx: number;
  tx: number;
  disk: number;
  players?: number;
  running: boolean;
};

export type Metrics = {
  latest: MetricPoint;
  points: MetricPoint[];
  limits: { memory_bytes: number; disk_bytes: number; cpu_percent: number };
};

export type ServerUse = { id: string; cpu: number; memory: number; players?: number };

export function useServerMetrics(nodeId: string, serverId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["metrics", nodeId, serverId],
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "server.metrics",
        paramsJson: JSON.stringify({ range: "1h" }),
      });
      return JSON.parse(res.resultJson) as Metrics;
    },
    enabled,
    refetchInterval: 10_000,
    placeholderData: (prev) => prev,
  });
}

// useNodeServers is what each of a node's servers uses now. Only admins and
// owners may ask (node.metrics), and only of a connected node.
export function useNodeServers(nodeId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["node-metrics", nodeId],
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        action: "node.metrics",
        paramsJson: JSON.stringify({ range: "1h" }),
      });
      const m = JSON.parse(res.resultJson) as { servers?: ServerUse[] };
      return new Map((m.servers ?? []).map((s) => [s.id, s]));
    },
    enabled,
    refetchInterval: 30_000,
    retry: false,
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

export const formatPercent = (n: number) => `${n < 10 ? n.toFixed(1) : Math.round(n)}%`;
