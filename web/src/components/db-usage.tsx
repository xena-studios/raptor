import { Activity, Clock, HardDrive, MemoryStick, Plug } from "lucide-react";

import { lastPoints, StatCard, UsageBar } from "@/components/stats";
import type { DBInstance } from "@/lib/databases";
import { type DBUsage, formatQueries, fraction, queriesNote } from "@/lib/db-usage";
import { formatBytes, formatUptime } from "@/lib/metrics";

// An instance's usage (docs/DATABASES.md#health-and-alerts), sampled by
// Wings every 15 seconds: a row of stats on its own page, like a server's,
// and a compact strip in the node's list of instances.

const uptime = (s: number) => (s < 60 ? `${s}s` : formatUptime(s));
const queries = (u?: DBUsage) =>
  u?.queries_per_second !== undefined ? formatQueries(u.queries_per_second) : "—";

export function InstanceUsageCards({
  instance: i,
  usage: u,
}: {
  instance: DBInstance;
  usage?: DBUsage;
}) {
  const recent = lastPoints(u?.recent, 40); // all 10 minutes of them
  const since = u?.uptime_seconds
    ? new Date(new Date(u.at).getTime() - u.uptime_seconds * 1000).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : undefined;
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
      <StatCard
        icon={MemoryStick}
        label="Memory"
        value={u ? formatBytes(u.memory_bytes) : "—"}
        sub={`of ${formatBytes(u?.memory_limit_bytes ?? i.limits.memory_mib * 1024 * 1024)}`}
        fraction={u ? fraction(u.memory_bytes, u.memory_limit_bytes) : undefined}
      />
      <StatCard
        icon={HardDrive}
        label="Disk"
        value={u?.disk_bytes ? formatBytes(u.disk_bytes) : "—"}
        sub={u?.disk_limit_bytes ? `of ${formatBytes(u.disk_limit_bytes)}` : "No limit"}
        fraction={u?.disk_bytes ? fraction(u.disk_bytes, u.disk_limit_bytes) : undefined}
      />
      <StatCard
        icon={Plug}
        label="Connections"
        value={u ? String(u.connections) : "—"}
        sub={u ? `of ${u.max_connections}` : " "}
        fraction={u ? fraction(u.connections, u.max_connections) : undefined}
        spark={recent.map((p) => p.connections)}
      />
      <StatCard
        icon={Activity}
        label="Queries"
        value={queries(u)}
        sub={queriesNote(i.engine)}
        spark={recent.map((p) => p.queries_per_second ?? null)}
      />
      <StatCard
        icon={Clock}
        label="Uptime"
        className="col-span-2 lg:col-span-1"
        value={u?.uptime_seconds ? uptime(u.uptime_seconds) : "—"}
        sub={since ? `Since ${since}` : "Since it started"}
      />
    </div>
  );
}

// InstanceUsageStrip is the same numbers, small, for a row in a list.
export function InstanceUsageStrip({
  instance: i,
  usage: u,
}: {
  instance: DBInstance;
  usage: DBUsage;
}) {
  const cells: { label: string; value: string; of?: string; f?: number; title?: string }[] = [
    {
      label: "Memory",
      value: formatBytes(u.memory_bytes),
      of: formatBytes(u.memory_limit_bytes),
      f: fraction(u.memory_bytes, u.memory_limit_bytes),
    },
    {
      label: "Disk",
      value: u.disk_bytes ? formatBytes(u.disk_bytes) : "—",
      of: u.disk_limit_bytes ? formatBytes(u.disk_limit_bytes) : undefined,
      f: u.disk_bytes ? fraction(u.disk_bytes, u.disk_limit_bytes) : undefined,
    },
    {
      label: "Connections",
      value: String(u.connections),
      of: String(u.max_connections),
      f: fraction(u.connections, u.max_connections),
    },
    { label: "Queries", value: queries(u), title: queriesNote(i.engine) },
    { label: "Uptime", value: u.uptime_seconds ? uptime(u.uptime_seconds) : "—" },
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 border-t pt-3 sm:grid-cols-3 lg:grid-cols-5">
      {cells.map((c) => (
        <div key={c.label} className="flex min-w-0 flex-col gap-1" title={c.title}>
          <dt className="font-mono text-[0.7rem] tracking-wider text-muted-foreground uppercase">
            {c.label}
          </dt>
          <dd className="truncate font-mono text-sm tabular-nums">
            {c.value}
            {c.of && <span className="text-muted-foreground"> / {c.of}</span>}
          </dd>
          {c.f !== undefined && <UsageBar fraction={c.f} />}
        </div>
      ))}
    </dl>
  );
}
