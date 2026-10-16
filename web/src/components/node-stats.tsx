import { Cpu, HardDrive, MemoryStick, Network, Users } from "lucide-react";
import { Graph, lastPoints, StatCard } from "@/components/stats";
import { TimeChart } from "@/components/time-chart";
import {
  formatBytes,
  formatPercent,
  formatRate,
  type MetricRange,
  metricRanges,
  type NodeMetrics,
} from "@/lib/metrics";

export function NodeStats({ m }: { m?: NodeMetrics }) {
  const l = m?.latest;
  const t = m?.totals;
  const recent = lastPoints(m?.points);
  const players = (m?.servers ?? []).reduce((n, s) => n + (s.players ?? 0), 0);
  const reporting = (m?.servers ?? []).some((s) => s.players !== undefined);
  const running = (m?.servers ?? []).filter((s) => s.state === "running").length;
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
      <StatCard
        icon={Cpu}
        label="CPU"
        value={l ? formatPercent(l.cpu) : "—"}
        sub={m ? `Load ${m.load[0].toFixed(2)} on ${t?.cpus} cores` : " "}
        spark={recent.map((p) => p.cpu)}
      />
      <StatCard
        icon={MemoryStick}
        label="Memory"
        value={l?.memory ? formatBytes(l.memory) : "—"}
        sub={t?.memory_bytes ? `of ${formatBytes(t.memory_bytes)}` : " "}
        fraction={l && t?.memory_bytes ? l.memory / t.memory_bytes : undefined}
      />
      <StatCard
        icon={HardDrive}
        label="Server storage"
        value={l?.disk ? formatBytes(l.disk) : "—"}
        sub={t?.disk_bytes ? `of ${formatBytes(t.disk_bytes)}` : " "}
        fraction={l && t?.disk_bytes ? l.disk / t.disk_bytes : undefined}
      />
      <StatCard
        icon={Network}
        label="Network"
        value={l ? `↓ ${formatRate(l.rx)}` : "—"}
        sub={l ? `↑ ${formatRate(l.tx)}` : " "}
        spark={recent.map((p) => p.rx + p.tx)}
      />
      <StatCard
        icon={Users}
        label="Players"
        className="col-span-2 lg:col-span-1"
        value={reporting ? String(players) : "—"}
        sub={`${running} ${running === 1 ? "server" : "servers"} running`}
      />
    </div>
  );
}

export function NodeGraphs({ m, range }: { m?: NodeMetrics; range: MetricRange }) {
  if (!m) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const span = metricRanges.find((r) => r.value === range)?.ms ?? 3600_000;
  const to = Date.now();
  const from = to - span;
  const one = (fn: (p: NodeMetrics["points"][number]) => number) =>
    m.points.map((p) => ({ t: p.at, values: [fn(p)] }));
  return (
    <div className="grid gap-8 md:grid-cols-2">
      <Graph title="CPU" note="Of the whole machine">
        <TimeChart
          data={one((p) => p.cpu)}
          series={[{ name: "CPU", color: "var(--series-1)" }]}
          format={formatPercent}
          minMax={10}
          from={from}
          to={to}
        />
      </Graph>
      <Graph title="Memory" note="In use, not counting cache">
        <TimeChart
          data={one((p) => p.memory)}
          series={[{ name: "Memory", color: "var(--series-1)" }]}
          format={formatBytes}
          limit={m.totals.memory_bytes}
          limitLabel="Total"
          minMax={1 << 30}
          from={from}
          to={to}
        />
      </Graph>
      <Graph title="Network">
        <TimeChart
          data={m.points.map((p) => ({ t: p.at, values: [p.rx, p.tx] }))}
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
      <Graph title="Load" note={`${m.totals.cpus} cores`}>
        <TimeChart
          data={one((p) => p.load)}
          series={[{ name: "Load", color: "var(--series-1)" }]}
          format={(v) => v.toFixed(v < 10 ? 1 : 0)}
          limit={m.totals.cpus}
          limitLabel="Cores"
          from={from}
          to={to}
        />
      </Graph>
    </div>
  );
}
