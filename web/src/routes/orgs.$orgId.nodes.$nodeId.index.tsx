import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Server } from "lucide-react";
import { type ReactNode, useState } from "react";

import { Hidden } from "@/components/address";
import { ChartSubject } from "@/components/chart-export";
import { LinkRow } from "@/components/entity-list";
import { NodeGraphs, NodeStats } from "@/components/node-stats";
import { DetailList, EmptyState } from "@/components/page";
import { PopoutButton } from "@/components/popout";
import { StatusBadge } from "@/components/server-status";
import { RangePicker } from "@/components/stats";
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
import { useIds } from "@/lib/ids";
import {
  formatBytes,
  formatPercent,
  formatUptime,
  type MetricRange,
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
  const { orgId, nodeId } = useIds();
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
      {showLive && <NodeStats m={live.data} />}
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
              <div className="min-w-0 flex-1 basis-xs space-y-1.5">
                <CardTitle>Resources</CardTitle>
                <CardDescription>
                  The whole machine: Raptor, its servers, and the rest.
                </CardDescription>
              </div>
              <div className="flex items-center gap-1">
                <RangePicker value={range} onChange={setRange} />
                <PopoutButton to={{ kind: "node-stats", orgId, nodeId }} label="the stats" />
              </div>
            </CardHeader>
            <CardContent>
              {history.error ? (
                <p className="text-sm text-destructive">{message(history.error)}</p>
              ) : (
                <ChartSubject value={node?.name}>
                  <NodeGraphs m={history.data} range={range} />
                </ChartSubject>
              )}
            </CardContent>
          </Card>
        ) : (
          <ServersCard
            className="lg:col-span-2"
            orgId={orgId}
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
        <ServersCard orgId={orgId} list={list} loaded={!!servers.data} connected use={use} />
      )}
    </div>
  );
}

function ServersCard({
  orgId,
  list,
  loaded,
  connected,
  use,
  className,
}: {
  orgId: string;
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
                      to: "/orgs/$orgId/servers/$serverId",
                      params: { orgId, serverId: s.id },
                    }}
                  >
                    <TableCell>
                      <Link
                        to="/orgs/$orgId/servers/$serverId"
                        params={{ orgId, serverId: s.id }}
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
