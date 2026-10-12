import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import {
  AlertTriangle,
  Archive,
  Building2,
  CircleDollarSign,
  Server,
  SquareTerminal,
  Users,
} from "lucide-react";
import { useState } from "react";

import { Graph, StatCard } from "@/components/stats";
import { TimeChart } from "@/components/time-chart";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type CountBucket, StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { formatBytes } from "@/lib/metrics";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/admin/")({
  component: Overview,
});

const ranges = [
  { days: 7, label: "7 days" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

const dollars = (cents: number | bigint) => {
  const c = Number(cents);
  return c % 100 === 0 ? `$${(c / 100).toLocaleString()}` : `$${(c / 100).toFixed(2)}`;
};

// The platform at a glance: what's connected and running, how it's grown,
// what Raptor Backup Storage would bill, and what needs looking at.
function Overview() {
  const [days, setDays] = useState(30);
  const ov = useQuery(StaffService.method.getOverview, { days }, { refetchInterval: 60_000 });
  if (ov.error) return <p className="text-sm text-destructive">{message(ov.error)}</p>;
  const d = ov.data;
  if (!d) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const c = d.counts;
  const hist = d.history.map((p) => ({ ...p, t: p.at ? timestampDate(p.at).getTime() : 0 }));
  const spark = (fn: (p: (typeof hist)[number]) => number) => hist.slice(-48).map(fn);
  const to = Date.now();
  const from = to - days * 86400_000;

  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <StatCard
          icon={SquareTerminal}
          label="Nodes connected"
          value={`${c?.nodesConnected ?? 0} / ${c?.nodes ?? 0}`}
          sub={
            c?.nodes
              ? `${Math.round(((c.nodesConnected ?? 0) / c.nodes) * 100)}% online`
              : "No nodes yet"
          }
          spark={spark((p) => p.nodesConnected)}
        />
        <StatCard
          icon={Server}
          label="Servers running"
          value={`${c?.serversRunning ?? 0} / ${c?.servers ?? 0}`}
          sub={`${d.failingServers.length} crashed or failed to install`}
          spark={spark((p) => p.serversRunning)}
        />
        <StatCard
          icon={Users}
          label="Users"
          value={String(c?.users ?? 0)}
          sub={`${c?.signups30d ?? 0} new, ${c?.activeUsers30d ?? 0} active in 30 days`}
          spark={spark((p) => p.users)}
        />
        <StatCard
          icon={Building2}
          label="Orgs"
          value={String(c?.orgs ?? 0)}
          sub={`${c?.storageNodes ?? 0} nodes on Backup Storage`}
          spark={spark((p) => p.orgs)}
        />
        <StatCard
          icon={Archive}
          label="Backup Storage"
          value={formatBytes(Number(c?.storageBytes ?? 0))}
          sub="Stored, measured daily"
          className="col-span-2 lg:col-span-1"
          spark={spark((p) => Number(p.storageBytes))}
        />
      </div>

      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
            <div className="space-y-1.5">
              <CardTitle>Growth</CardTitle>
              <CardDescription>Counted every hour.</CardDescription>
            </div>
            <fieldset className="flex rounded-lg border p-0.5">
              <legend className="sr-only">Time range</legend>
              {ranges.map((r) => (
                <button
                  key={r.days}
                  type="button"
                  aria-pressed={days === r.days}
                  onClick={() => setDays(r.days)}
                  className={cn(
                    "rounded-md px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground",
                    days === r.days && "bg-muted font-medium text-foreground",
                  )}
                >
                  {r.label}
                </button>
              ))}
            </fieldset>
          </CardHeader>
          <CardContent className="grid gap-8 md:grid-cols-2">
            <Graph title="Nodes">
              <TimeChart
                data={hist.map((p) => ({ t: p.t, values: [p.nodes, p.nodesConnected] }))}
                series={[
                  { name: "Linked", color: "var(--series-2)" },
                  { name: "Connected", color: "var(--series-1)" },
                ]}
                format={(v) => String(Math.round(v))}
                minMax={4}
                from={from}
                to={to}
                gap={3 * 3600_000}
                dots
              />
            </Graph>
            <Graph title="Servers">
              <TimeChart
                data={hist.map((p) => ({ t: p.t, values: [p.servers, p.serversRunning] }))}
                series={[
                  { name: "All", color: "var(--series-2)" },
                  { name: "Running", color: "var(--series-1)" },
                ]}
                format={(v) => String(Math.round(v))}
                minMax={4}
                from={from}
                to={to}
                gap={3 * 3600_000}
                dots
              />
            </Graph>
            <Graph title="Users">
              <TimeChart
                data={hist.map((p) => ({ t: p.t, values: [p.users] }))}
                series={[{ name: "Users", color: "var(--series-1)" }]}
                format={(v) => String(Math.round(v))}
                minMax={4}
                from={from}
                to={to}
                gap={3 * 3600_000}
                dots
              />
            </Graph>
            <Graph title="Sign-ups" note="Per day">
              <TimeChart
                data={d.signupsByDay.map((b) => ({
                  t: new Date(`${b.name}T12:00:00Z`).getTime(),
                  values: [b.count],
                }))}
                series={[{ name: "Sign-ups", color: "var(--series-1)" }]}
                format={(v) => String(Math.round(v))}
                minMax={4}
                from={from}
                to={to}
                gap={2 * 86400_000}
                dots
              />
            </Graph>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-6">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <CircleDollarSign className="size-4" /> Revenue
              </CardTitle>
              <CardDescription>
                {d.revenue?.billingLive
                  ? "From billing."
                  : "Billing isn't live yet, so nothing is charged. This is what Raptor Backup Storage would bill a month at today's usage."}
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-1">
              <p className="text-2xl font-semibold tabular-nums">
                {dollars(d.revenue?.storageCentsMonth ?? 0n)}
                <span className="ml-1 text-sm font-normal text-muted-foreground">/ month</span>
              </p>
              <p className="text-xs text-muted-foreground">
                Backup Storage, from {d.revenue?.storageOrgs ?? 0} orgs (10 GB a node included, then
                $12 a TB).
              </p>
            </CardContent>
          </Card>
          <Breakdown title="Wings versions" buckets={d.wingsVersions} />
          <Breakdown title="Most deployed games" buckets={d.topEggs} />
          <Breakdown title="Architectures" buckets={d.archs} />
        </div>
      </div>

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle className="size-4 text-amber-500" /> Offline nodes
            </CardTitle>
            <CardDescription>Gone for over an hour, most recently seen first.</CardDescription>
          </CardHeader>
          <CardContent>
            {d.offlineNodes.length === 0 ? (
              <p className="text-sm text-muted-foreground">None.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Org</TableHead>
                    <TableHead className="text-right">Last seen</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.offlineNodes.map((n) => (
                    <TableRow key={n.nodeId}>
                      <TableCell className="font-medium">{n.name}</TableCell>
                      <TableCell>
                        <Link
                          to="/admin/orgs/$orgId"
                          params={{ orgId: n.orgId }}
                          className="hover:underline"
                        >
                          {n.orgName}
                        </Link>
                      </TableCell>
                      <TableCell className="text-right text-muted-foreground">
                        {when(n.lastSeenAt)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle className="size-4 text-destructive" /> Failing servers
            </CardTitle>
            <CardDescription>
              Crashed, or failed to install, as their nodes last said.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {d.failingServers.length === 0 ? (
              <p className="text-sm text-muted-foreground">None.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Server</TableHead>
                    <TableHead>Game</TableHead>
                    <TableHead>Org</TableHead>
                    <TableHead className="text-right">State</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.failingServers.map((s) => (
                    <TableRow key={`${s.nodeId}/${s.serverId}`}>
                      <TableCell className="font-medium">{s.name}</TableCell>
                      <TableCell className="text-muted-foreground">{s.eggName || "—"}</TableCell>
                      <TableCell>
                        <Link
                          to="/admin/orgs/$orgId"
                          params={{ orgId: s.orgId }}
                          className="hover:underline"
                        >
                          {s.orgName}
                        </Link>
                      </TableCell>
                      <TableCell
                        className="text-right text-destructive"
                        title={s.installError || undefined}
                      >
                        {s.state === "install_failed" ? "Install failed" : "Crashed"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

// Breakdown is a count per name, as bars.
function Breakdown({ title, buckets }: { title: string; buckets: CountBucket[] }) {
  const total = buckets.reduce((n, b) => n + b.count, 0);
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {buckets.length === 0 && <p className="text-sm text-muted-foreground">Nothing yet.</p>}
        {buckets.map((b) => (
          <div key={b.name} className="flex flex-col gap-1">
            <div className="flex justify-between gap-3 text-sm">
              <span className="truncate">{b.name}</span>
              <span className="tabular-nums text-muted-foreground">{b.count}</span>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-[var(--series-1)]"
                style={{ width: `${total ? (b.count / total) * 100 : 0}%` }}
              />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
