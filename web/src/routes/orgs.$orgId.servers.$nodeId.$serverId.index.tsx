import { createFileRoute } from "@tanstack/react-router";
import { Cpu, HardDrive, type LucideIcon, MemoryStick, Network, Users } from "lucide-react";
import { lazy, type ReactNode, Suspense, useState } from "react";

import { DetailList } from "@/components/page";
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
import { cn } from "@/lib/utils";

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
  const address =
    node && server.ports[0] ? `n-${node.shortId}.raptornodes.net:${server.ports[0]}` : "—";
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
                  <span key="c" className="font-mono text-xs">
                    {address}
                  </span>,
                ],
                ["Game", server.eggName || "—"],
                ["Node", node?.name ?? "—"],
                ["SFTP ID", <code key="s">{server.id.slice(-8)}</code>],
              ]}
            />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Resources</CardTitle>
            <CardDescription>
              Sampled every 10 seconds; minutes for a day, then 15 minutes for a week.
            </CardDescription>
          </div>
          <fieldset className="flex rounded-lg border p-0.5">
            <legend className="sr-only">Time range</legend>
            {metricRanges.map((r) => (
              <button
                key={r.value}
                type="button"
                aria-pressed={range === r.value}
                onClick={() => setRange(r.value)}
                className={cn(
                  "rounded-md px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground",
                  range === r.value && "bg-muted font-medium text-foreground",
                )}
              >
                {r.label}
              </button>
            ))}
          </fieldset>
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
  const playersToday = Math.max(0, ...(metrics?.points ?? []).map((x) => x.players_max ?? 0));
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
      <Stat
        icon={Cpu}
        label="CPU"
        value={live ? formatPercent(p.cpu) : "—"}
        sub={l?.cpu_percent ? `of ${l.cpu_percent}% limit` : "100% is one core"}
        fraction={live && l?.cpu_percent ? p.cpu / l.cpu_percent : undefined}
      />
      <Stat
        icon={MemoryStick}
        label="Memory"
        value={live ? formatBytes(p.memory) : "—"}
        sub={l ? `of ${formatBytes(l.memory_bytes)}` : ""}
        fraction={live && l?.memory_bytes ? p.memory / l.memory_bytes : undefined}
      />
      <Stat
        icon={HardDrive}
        label="Disk"
        value={p?.disk ? formatBytes(p.disk) : "—"}
        sub={l?.disk_bytes ? `of ${formatBytes(l.disk_bytes)}` : "No limit"}
        fraction={p?.disk && l?.disk_bytes ? p.disk / l.disk_bytes : undefined}
      />
      <Stat
        icon={Network}
        label="Network"
        value={live ? `↓ ${formatRate(p.rx)}` : "—"}
        sub={live ? `↑ ${formatRate(p.tx)}` : "In and out"}
      />
      <Stat
        icon={Users}
        label="Players"
        value={live && p.players !== undefined ? String(Math.round(p.players)) : "—"}
        sub={
          p?.players === undefined && live
            ? "This game doesn't report players"
            : `Most in the last hour: ${playersToday}`
        }
      />
    </div>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  sub,
  fraction,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  sub: string;
  fraction?: number;
}) {
  const f = fraction === undefined ? undefined : Math.min(1, Math.max(0, fraction));
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-1.5">
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Icon className="size-3.5" /> {label}
        </p>
        <p className="text-xl font-semibold tabular-nums">{value}</p>
        {f !== undefined && (
          <div className="h-1 overflow-hidden rounded-full bg-muted">
            <div
              className={cn(
                "h-full rounded-full transition-[width]",
                f > 0.9 ? "bg-destructive" : "bg-[var(--series-1)]",
              )}
              style={{ width: `${f * 100}%` }}
            />
          </div>
        )}
        <p className="truncate text-xs text-muted-foreground">{sub}</p>
      </CardContent>
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

function Graph({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">
        {title}
        {note && <span className="ml-2 text-xs font-normal text-muted-foreground">{note}</span>}
      </h3>
      {children}
    </section>
  );
}
