import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Cpu, HardDrive, MemoryStick, Network, Server, Users } from "lucide-react";
import { type ReactNode, useState } from "react";

import { Hidden } from "@/components/address";
import { LinkRow } from "@/components/entity-list";
import { DetailList, EmptyState } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { Graph, lastPoints, RangePicker, StatCard } from "@/components/stats";
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
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { nodeHostname } from "@/lib/addresses";
import { message } from "@/lib/errors";
import { isAdmin, since, when } from "@/lib/format";
import {
  formatBytes,
  formatPercent,
  formatRate,
  formatUptime,
  type MetricRange,
  metricRanges,
  type NodeMetrics,
  type ServerUse,
  useNodeMetrics,
} from "@/lib/metrics";
import { formatMemory, useOrgNodes } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/")({
  component: NodeOverview,
});

// The node's overview: the machine now and over time (admins), what each
// server on it uses, and what the machine is.
function NodeOverview() {
  const { orgId, nodeId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const nodes = useOrgNodes(orgId);
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  const servers = useQuery(
    OrgService.method.listServers,
    { orgId, nodeId },
    { refetchInterval: 5_000 },
  );
  const [range, setRange] = useState<MetricRange>("1h");
  const live = useNodeMetrics(nodeId, "1h", admin && !!node?.connected);
  const history = useNodeMetrics(nodeId, range, admin && !!node?.connected);
  const list = servers.data?.servers ?? [];
  const use = new Map((live.data?.servers ?? []).map((u) => [u.id, u]));
  const showLive = admin && !!node?.connected;

  return (
    <div className="flex flex-col gap-6">
      {showLive && <Stats m={live.data} />}
      {node && !node.connected && (
        <Card size="sm">
          <CardContent className="text-sm text-muted-foreground">
            {node.name} is offline (last seen {since(node.lastSeenAt)}). Its servers keep running if
            the machine is up; what's shown is its last report.
          </CardContent>
        </Card>
      )}

      <div className="grid items-start gap-6 lg:grid-cols-3">
        {showLive ? (
          <Card className="lg:col-span-2">
            <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
              <div className="space-y-1.5">
                <CardTitle>Resources</CardTitle>
                <CardDescription>
                  The whole machine: Raptor, its servers, and the rest.
                </CardDescription>
              </div>
              <RangePicker value={range} onChange={setRange} />
            </CardHeader>
            <CardContent>
              {history.error ? (
                <p className="text-sm text-destructive">{message(history.error)}</p>
              ) : (
                <Graphs m={history.data} range={range} />
              )}
            </CardContent>
          </Card>
        ) : (
          <ServersCard
            className="lg:col-span-2"
            orgId={orgId}
            nodeId={nodeId}
            list={list}
            loaded={!!servers.data}
            connected={!!node?.connected}
            use={use}
          />
        )}
        <Card>
          <CardHeader>
            <CardTitle>Details</CardTitle>
            <CardDescription>The machine, as it reported itself.</CardDescription>
          </CardHeader>
          <CardContent>
            {node && (
              <DetailList
                rows={[
                  ...(node.displayAddress
                    ? [
                        [
                          "Shown address",
                          <code key="d" className="text-xs">
                            {node.displayAddress}
                          </code>,
                        ] as [string, ReactNode],
                      ]
                    : []),
                  ["Hostname", <Hidden key="h" value={nodeHostname(node)} label="hostname" />],
                  ...(node.publicIpv4
                    ? [
                        ["IPv4", <Hidden key="4" value={node.publicIpv4} label="IP address" />] as [
                          string,
                          ReactNode,
                        ],
                      ]
                    : []),
                  ...(node.publicIpv6
                    ? [
                        ["IPv6", <Hidden key="6" value={node.publicIpv6} label="IP address" />] as [
                          string,
                          ReactNode,
                        ],
                      ]
                    : []),
                  ["Architecture", node.arch || "—"],
                  ["Cores", node.cpus ? String(node.cpus) : "—"],
                  ["Memory", formatMemory(node.memoryBytes)],
                  ...(live.data?.totals.disk_bytes
                    ? [
                        ["Server storage", formatBytes(live.data.totals.disk_bytes)] as [
                          string,
                          string,
                        ],
                      ]
                    : []),
                  ["Wings", node.wingsVersion || "—"],
                  ...(showLive && live.data
                    ? [["Up for", formatUptime(live.data.uptime)] as [string, string]]
                    : []),
                  [
                    "Servers",
                    `${list.filter((s) => s.state === "running").length} running of ${list.length}`,
                  ],
                  ["Last seen", node.connected ? "Now" : since(node.lastSeenAt)],
                  ["Linked", when(node.createdAt)],
                ]}
              />
            )}
          </CardContent>
        </Card>
      </div>

      {showLive && (
        <ServersCard
          orgId={orgId}
          nodeId={nodeId}
          list={list}
          loaded={!!servers.data}
          connected
          use={use}
        />
      )}
    </div>
  );
}

function Stats({ m }: { m?: NodeMetrics }) {
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

function Graphs({ m, range }: { m?: NodeMetrics; range: MetricRange }) {
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

function ServersCard({
  orgId,
  nodeId,
  list,
  loaded,
  connected,
  use,
  className,
}: {
  orgId: string;
  nodeId: string;
  list: {
    id: string;
    name: string;
    eggName: string;
    ports: number[];
    state: string;
    installState: string;
    installError: string;
  }[];
  loaded: boolean;
  connected: boolean;
  use: Map<string, ServerUse>;
  className?: string;
}) {
  const live = use.size > 0;
  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>Servers</CardTitle>
        <CardDescription>
          {live ? "What each server on this node uses right now." : "What this node runs."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {loaded && list.length === 0 ? (
          <EmptyState icon={Server} title="No servers on this node" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Server</TableHead>
                <TableHead className="hidden sm:table-cell">Game</TableHead>
                <TableHead className="hidden md:table-cell">Port</TableHead>
                {live && <TableHead className="text-right">CPU</TableHead>}
                {live && <TableHead className="hidden text-right sm:table-cell">Memory</TableHead>}
                {live && <TableHead className="hidden text-right md:table-cell">Players</TableHead>}
                <TableHead className="text-right">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((s) => {
                const u = use.get(s.id);
                const on = u?.state === "running";
                return (
                  <LinkRow
                    key={s.id}
                    link={{
                      to: "/orgs/$orgId/servers/$nodeId/$serverId",
                      params: { orgId, nodeId, serverId: s.id },
                    }}
                  >
                    <TableCell>
                      <Link
                        to="/orgs/$orgId/servers/$nodeId/$serverId"
                        params={{ orgId, nodeId, serverId: s.id }}
                        className="font-medium hover:underline"
                      >
                        {s.name}
                      </Link>
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground sm:table-cell">
                      {s.eggName || "—"}
                    </TableCell>
                    <TableCell className="hidden font-mono text-xs tabular-nums md:table-cell">
                      {s.ports[0] ?? "—"}
                    </TableCell>
                    {live && (
                      <TableCell className="text-right font-mono text-xs tabular-nums">
                        {on ? formatPercent(u.cpu) : "—"}
                      </TableCell>
                    )}
                    {live && (
                      <TableCell className="hidden text-right font-mono text-xs tabular-nums sm:table-cell">
                        {on ? formatBytes(u.memory) : "—"}
                      </TableCell>
                    )}
                    {live && (
                      <TableCell className="hidden text-right font-mono text-xs tabular-nums md:table-cell">
                        {on && u.players !== undefined ? u.players : "—"}
                      </TableCell>
                    )}
                    <TableCell className="text-right">
                      <StatusBadge status={serverStatus(s, connected)} />
                    </TableCell>
                  </LinkRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
