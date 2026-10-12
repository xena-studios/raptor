import { createFileRoute } from "@tanstack/react-router";
import { Cpu, HardDrive, MemoryStick, Network, Users } from "lucide-react";
import { lazy, Suspense, useState } from "react";

import { ServerAddress } from "@/components/address";
import { DetailList } from "@/components/page";
import { Graph, lastPoints, RangePicker, StatCard } from "@/components/stats";
import { TimeChart } from "@/components/time-chart";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { message } from "@/lib/errors";
import {
  formatBytes,
  formatPercent,
  formatRate,
  type MetricRange,
  type Metrics,
  metricRanges,
  useMetrics,
} from "@/lib/metrics";
import { can, useServer } from "@/lib/org-data";

const Console = lazy(() => import("@/components/console"));

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/")({
  component: Overview,
});

// The server's overview: how it's doing now, its console, where players
// reach it, and its resources over time.
function Overview() {
  const { orgId, nodeId, serverId } = Route.useParams();
  const { node, server } = useServer(orgId, nodeId, serverId);
  const [range, setRange] = useState<MetricRange>("1h");
  const live = useMetrics(nodeId, serverId, "1h");
  const history = useMetrics(nodeId, serverId, range);
  if (!server) return null;
  const running = server.state === "running" || server.state === "starting";

  return (
    <div className="flex flex-col gap-6">
      <Stats metrics={live.data} running={running && !!node?.connected} />

      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Console</CardTitle>
            <CardDescription>Live output from your server.</CardDescription>
          </CardHeader>
          <CardContent>
            {can(server, "console.read") || can(server, "console.write") ? (
              <Suspense
                fallback={<p className="text-sm text-muted-foreground">Loading the console…</p>}
              >
                <Console
                  nodeId={nodeId}
                  serverId={serverId}
                  canWrite={can(server, "console.write")}
                />
              </Suspense>
            ) : (
              <p className="text-sm text-muted-foreground">
                You don't have access to this server's console.
              </p>
            )}
          </CardContent>
        </Card>
        <div className="flex flex-col gap-6">
          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
              <CardDescription>Where players reach this server.</CardDescription>
            </CardHeader>
            <CardContent>
              <DetailList
                rows={[
                  [
                    "Connect",
                    node ? <ServerAddress key="c" node={node} port={server.ports[0]} /> : "—",
                  ],
                  ["Game", server.eggName || "—"],
                  ["Node", node?.name ?? "—"],
                  ["SFTP ID", <code key="s">{server.id.slice(-8)}</code>],
                ]}
              />
            </CardContent>
          </Card>
          <LastHour metrics={live.data} />
        </div>
      </div>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Resources</CardTitle>
            <CardDescription>
              Sampled every 10 seconds; minutes for a day, then 15 minutes for a week.
            </CardDescription>
          </div>
          <RangePicker value={range} onChange={setRange} />
        </CardHeader>
        <CardContent>
          {history.error ? (
            <p className="text-sm text-destructive">{message(history.error)}</p>
          ) : (
            <Graphs metrics={history.data} range={range} />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function Stats({ metrics, running }: { metrics?: Metrics; running: boolean }) {
  const p = metrics?.latest;
  const l = metrics?.limits;
  const live = running && p?.running;
  const recent = lastPoints(metrics?.points).map((x) => (x.running ? x : null));
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
      <StatCard
        icon={Cpu}
        label="CPU"
        value={live ? formatPercent(p.cpu) : "—"}
        sub={l?.cpu_percent ? `of ${l.cpu_percent}% limit` : "100% is one core"}
        fraction={live && l?.cpu_percent ? p.cpu / l.cpu_percent : undefined}
        spark={recent.map((x) => x?.cpu ?? null)}
      />
      <StatCard
        icon={MemoryStick}
        label="Memory"
        value={live ? formatBytes(p.memory) : "—"}
        sub={l ? `of ${formatBytes(l.memory_bytes)}` : ""}
        fraction={live && l?.memory_bytes ? p.memory / l.memory_bytes : undefined}
      />
      <StatCard
        icon={HardDrive}
        label="Disk"
        value={p?.disk ? formatBytes(p.disk) : "—"}
        sub={l?.disk_bytes ? `of ${formatBytes(l.disk_bytes)}` : "No limit"}
        fraction={p?.disk && l?.disk_bytes ? p.disk / l.disk_bytes : undefined}
      />
      <StatCard
        icon={Network}
        label="Network"
        value={live ? `↓ ${formatRate(p.rx)}` : "—"}
        sub={live ? `↑ ${formatRate(p.tx)}` : "In and out"}
        spark={recent.map((x) => (x ? x.rx + x.tx : null))}
      />
      <StatCard
        icon={Users}
        label="Players"
        className="col-span-2 lg:col-span-1"
        value={live && p.players !== undefined ? String(Math.round(p.players)) : "—"}
        sub={p?.players === undefined && live ? "This game doesn't report players" : "Online now"}
        spark={
          recent.some((x) => x?.players !== undefined)
            ? recent.map((x) => x?.players ?? null)
            : undefined
        }
      />
    </div>
  );
}

// LastHour sums up the last hour: peaks, traffic, and the most players.
function LastHour({ metrics }: { metrics?: Metrics }) {
  const pts = (metrics?.points ?? []).filter((p) => p.running);
  const minutes = pts.reduce((n, p) => n + (p.resolution || 60), 0) / 60;
  const peakCPU = Math.max(0, ...pts.map((p) => p.cpu_max));
  const peakMem = Math.max(0, ...pts.map((p) => p.memory_max));
  const rx = pts.reduce((n, p) => n + p.rx * (p.resolution || 60), 0);
  const tx = pts.reduce((n, p) => n + p.tx * (p.resolution || 60), 0);
  const players = pts.some((p) => p.players_max !== undefined)
    ? Math.max(0, ...pts.map((p) => p.players_max ?? 0))
    : undefined;
  return (
    <Card>
      <CardHeader>
        <CardTitle>The last hour</CardTitle>
        <CardDescription>
          {pts.length === 0
            ? "It hasn't run in the last hour."
            : `Running for ${Math.round(minutes)} of the last 60 minutes.`}
        </CardDescription>
      </CardHeader>
      {pts.length > 0 && (
        <CardContent>
          <DetailList
            rows={[
              ["Peak CPU", formatPercent(peakCPU)],
              ["Peak memory", formatBytes(peakMem)],
              ["Data in", formatBytes(rx)],
              ["Data out", formatBytes(tx)],
              ...(players !== undefined
                ? [["Most players", String(players)] as [string, string]]
                : []),
            ]}
          />
        </CardContent>
      )}
    </Card>
  );
}

function Graphs({ metrics, range }: { metrics?: Metrics; range: MetricRange }) {
  if (!metrics) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const span = metricRanges.find((r) => r.value === range)?.ms ?? 3600_000;
  const to = Date.now();
  const from = to - span;
  const pts = metrics.points;
  // Stopped stretches have no samples: gaps, not zeros.
  const at = (fn: (p: (typeof pts)[number]) => number | undefined) =>
    pts.map((p) => ({ t: p.at, values: [p.running ? (fn(p) ?? null) : null] }));
  const hasPlayers = pts.some((p) => p.players !== undefined);
  return (
    <div className="grid gap-8 md:grid-cols-2">
      <Graph title="CPU" note="Average per point; 100% is one core">
        <TimeChart
          data={at((p) => p.cpu)}
          series={[{ name: "CPU", color: "var(--series-1)" }]}
          format={formatPercent}
          limit={metrics.limits.cpu_percent || undefined}
          from={from}
          to={to}
        />
      </Graph>
      <Graph title="Memory">
        <TimeChart
          data={at((p) => p.memory)}
          series={[{ name: "Memory", color: "var(--series-1)" }]}
          format={formatBytes}
          limit={metrics.limits.memory_bytes}
          minMax={1 << 20}
          from={from}
          to={to}
        />
      </Graph>
      <Graph title="Network">
        <TimeChart
          data={pts.map((p) => ({
            t: p.at,
            values: p.running ? [p.rx, p.tx] : [null, null],
          }))}
          series={[
            { name: "In", color: "var(--series-1)" },
            { name: "Out", color: "var(--series-2)" },
          ]}
          format={formatRate}
          minMax={1000}
          from={from}
          to={to}
        />
      </Graph>
      {hasPlayers ? (
        <Graph title="Players">
          <TimeChart
            data={at((p) => p.players)}
            series={[{ name: "Players", color: "var(--series-1)" }]}
            format={(v) => String(Math.round(v))}
            minMax={4}
            from={from}
            to={to}
          />
        </Graph>
      ) : (
        <Graph title="Disk">
          <TimeChart
            data={pts.map((p) => ({ t: p.at, values: [p.disk || null] }))}
            series={[{ name: "Disk", color: "var(--series-1)" }]}
            format={formatBytes}
            limit={metrics.limits.disk_bytes || undefined}
            from={from}
            to={to}
          />
        </Graph>
      )}
    </div>
  );
}
