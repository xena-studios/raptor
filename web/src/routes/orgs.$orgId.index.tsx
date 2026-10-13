import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import {
  AlertTriangle,
  CheckCircle2,
  Cpu,
  MemoryStick,
  Plus,
  Server,
  SquareTerminal,
  Users,
} from "lucide-react";

import { OrgActivity } from "@/components/org/audit-log";
import { EmptyState, PageHeader } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { StatCard, UsageBar } from "@/components/stats";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin, since } from "@/lib/format";
import {
  formatBytes,
  formatPercent,
  type NodeMetrics,
  type ServerUse,
  useNodesMetrics,
} from "@/lib/metrics";
import { type OrgServer, useOrgNodes, useOrgServers } from "@/lib/org-data";
import { nodeStatus, serverStatus } from "@/lib/servers";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/orgs/$orgId/")({
  component: Overview,
});

const shown = 10;

// The org's overview: what's running and how hard the machines work, what
// needs attention, and the latest activity. Admins see the machines' live
// numbers (node.metrics); members see their servers.
function Overview() {
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const nodes = useOrgNodes(orgId);
  const { servers } = useOrgServers(orgId);
  const nodeList = nodes.data?.nodes ?? [];
  const connected = nodeList.filter((n) => n.connected);
  const admin = isAdmin(org?.role);
  const results = useNodesMetrics(
    connected.map((n) => n.id),
    admin,
  );
  const metrics = new Map<string, NodeMetrics>();
  results.forEach((r, i) => {
    const n = connected[i];
    if (n && r.data) metrics.set(n.id, r.data);
  });
  const use = new Map<string, ServerUse>();
  for (const [nodeId, m] of metrics) for (const u of m.servers) use.set(`${nodeId}/${u.id}`, u);

  const running = servers.filter((s) => s.state === "running").length;
  const failing = servers.filter((s) => ["crashed", "install_failed"].includes(s.state));
  const offline = nodeList.filter((n) => !n.connected);
  const live = metrics.size > 0;

  return (
    <>
      <PageHeader
        eyebrow={org?.name}
        title="Overview"
        description="Your nodes and servers at a glance."
        actions={
          admin && (
            <Link
              to="/orgs/$orgId/servers/new"
              params={{ orgId }}
              className={buttonVariants({ size: "sm" })}
            >
              <Plus /> Deploy server
            </Link>
          )
        }
      />
      {nodes.data && nodeList.length === 0 ? (
        <EmptyState
          icon={SquareTerminal}
          title="No nodes yet"
          description="Connect a machine to start running servers on it."
          action={
            admin && (
              <Link
                to="/orgs/$orgId/nodes/new"
                params={{ orgId }}
                className={buttonVariants({ size: "sm" })}
              >
                <Plus /> Connect node
              </Link>
            )
          }
        />
      ) : (
        <>
          <Stats
            servers={servers.length}
            running={running}
            failing={failing.length}
            nodes={nodeList}
            metrics={live ? metrics : undefined}
          />
          <div className="grid items-start gap-6 lg:grid-cols-3">
            <ServersCard
              className="lg:col-span-2"
              orgId={orgId}
              servers={servers}
              use={use}
              live={live}
            />
            <div className="flex flex-col gap-6">
              <Attention orgId={orgId} failing={failing} offline={offline} />
              <NodesCard orgId={orgId} nodes={nodeList} metrics={metrics} />
            </div>
          </div>
          {admin && (
            <OrgActivity
              orgId={orgId}
              limit={6}
              action={
                <Link
                  to="/orgs/$orgId/settings/activity"
                  params={{ orgId }}
                  className={buttonVariants({ size: "sm", variant: "outline" })}
                >
                  All activity
                </Link>
              }
            />
          )}
        </>
      )}
    </>
  );
}

function Stats({
  servers,
  running,
  failing,
  nodes,
  metrics,
}: {
  servers: number;
  running: number;
  failing: number;
  nodes: Node[];
  metrics?: Map<string, NodeMetrics>;
}) {
  const online = nodes.filter((n) => n.connected).length;
  const ms = [...(metrics?.values() ?? [])];
  const cores = ms.reduce((n, m) => n + m.totals.cpus, 0);
  const cpu = cores ? ms.reduce((n, m) => n + m.latest.cpu * m.totals.cpus, 0) / cores : 0;
  const memUsed = ms.reduce((n, m) => n + m.latest.memory, 0);
  const memTotal = ms.reduce((n, m) => n + m.totals.memory_bytes, 0);
  const players = ms.flatMap((m) => m.servers).filter((s) => s.players !== undefined);
  return (
    <div className={cn("grid grid-cols-2 gap-4", metrics && "lg:grid-cols-4")}>
      <StatCard
        icon={Server}
        label="Servers running"
        value={`${running} / ${servers}`}
        sub={failing ? `${failing} need attention` : "None need attention"}
      />
      {metrics ? (
        <StatCard
          icon={Users}
          label="Players online"
          value={players.length ? String(players.reduce((n, s) => n + (s.players ?? 0), 0)) : "—"}
          sub={
            players.length
              ? `On ${players.length} ${players.length === 1 ? "server" : "servers"} that report them`
              : "No running server reports players"
          }
        />
      ) : (
        <StatCard
          icon={SquareTerminal}
          label="Nodes online"
          value={`${online} / ${nodes.length}`}
          sub={online === nodes.length ? "All connected" : `${nodes.length - online} offline`}
        />
      )}
      {metrics && (
        <StatCard
          icon={Cpu}
          label="CPU"
          value={formatPercent(cpu)}
          sub={`${cores} cores across ${ms.length} ${ms.length === 1 ? "node" : "nodes"}`}
          spark={combinedCPU(ms)}
        />
      )}
      {metrics && (
        <StatCard
          icon={MemoryStick}
          label="Memory"
          value={formatBytes(memUsed)}
          sub={`of ${formatBytes(memTotal)}`}
          fraction={memTotal ? memUsed / memTotal : undefined}
        />
      )}
    </div>
  );
}

// combinedCPU is the last half hour of every node's CPU, weighted by cores.
function combinedCPU(ms: NodeMetrics[]): number[] {
  const at = new Map<number, { sum: number; cores: number }>();
  for (const m of ms) {
    for (const p of m.points.slice(-30)) {
      const a = at.get(p.at) ?? { sum: 0, cores: 0 };
      a.sum += p.cpu * m.totals.cpus;
      a.cores += m.totals.cpus;
      at.set(p.at, a);
    }
  }
  return [...at.entries()].sort(([a], [b]) => a - b).map(([, a]) => a.sum / a.cores);
}

function ServersCard({
  orgId,
  servers,
  use,
  live,
  className,
}: {
  orgId: string;
  servers: OrgServer[];
  use: Map<string, ServerUse>;
  live: boolean;
  className?: string;
}) {
  // Running first, then by name.
  const sorted = [...servers].sort(
    (a, b) =>
      Number(b.state === "running") - Number(a.state === "running") || a.name.localeCompare(b.name),
  );
  return (
    <Card className={className}>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle>Servers</CardTitle>
          <CardDescription>
            {live ? "What's running, and what each uses right now." : "What's running."}
          </CardDescription>
        </div>
        {servers.length > shown && (
          <Link
            to="/orgs/$orgId/servers"
            params={{ orgId }}
            className={buttonVariants({ size: "sm", variant: "outline" })}
          >
            All {servers.length}
          </Link>
        )}
      </CardHeader>
      <CardContent>
        {servers.length === 0 ? (
          <p className="text-sm text-muted-foreground">No servers yet.</p>
        ) : (
          <ul className="flex flex-col divide-y">
            {live && (
              <li
                aria-hidden
                className="hidden justify-end gap-5 pb-2 pr-[6.75rem] text-right text-xs text-muted-foreground sm:flex"
              >
                <span className="w-16">CPU</span>
                <span className="w-16">Memory</span>
                <span className="w-16">Players</span>
              </li>
            )}
            {sorted.slice(0, shown).map((s) => {
              const u = use.get(`${s.node.id}/${s.id}`);
              const on = u?.state === "running";
              return (
                <li key={`${s.node.id}/${s.id}`}>
                  <Link
                    to="/orgs/$orgId/servers/$nodeId/$serverId"
                    params={{ orgId, nodeId: s.node.id, serverId: s.id }}
                    className="-mx-2 flex items-center gap-3 rounded-md px-2 py-2.5 text-sm hover:bg-muted/50"
                  >
                    <div className="min-w-0 flex-1">
                      <p className="truncate font-medium">{s.name}</p>
                      <p className="truncate text-xs text-muted-foreground">
                        {[s.eggName, s.node.name].filter(Boolean).join(" · ")}
                      </p>
                    </div>
                    {live && (
                      <div className="hidden gap-5 text-right tabular-nums sm:flex">
                        <Figure value={on ? formatPercent(u.cpu) : "—"} />
                        <Figure value={on ? formatBytes(u.memory) : "—"} />
                        <Figure value={on && u.players !== undefined ? String(u.players) : "—"} />
                      </div>
                    )}
                    <span className="flex w-24 shrink-0 justify-end">
                      <StatusBadge status={serverStatus(s, s.node.connected)} />
                    </span>
                  </Link>
                </li>
              );
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function Figure({ value }: { value: string }) {
  return <span className="w-16 text-sm">{value}</span>;
}

// Attention lists what's wrong: servers that crashed or failed to install,
// and nodes that are offline.
function Attention({
  orgId,
  failing,
  offline,
}: {
  orgId: string;
  failing: OrgServer[];
  offline: Node[];
}) {
  const none = failing.length === 0 && offline.length === 0;
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>{none ? "All good" : "Needs attention"}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 text-sm">
        {none && (
          <p className="flex items-center gap-2 text-muted-foreground">
            <CheckCircle2 className="size-4 text-ok" />
            Every node is connected and no server has crashed.
          </p>
        )}
        {failing.slice(0, 4).map((s) => (
          <Link
            key={`${s.node.id}/${s.id}`}
            to="/orgs/$orgId/servers/$nodeId/$serverId"
            params={{ orgId, nodeId: s.node.id, serverId: s.id }}
            className="flex items-start gap-2 hover:underline"
          >
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
            <span className="min-w-0">
              <span className="font-medium">{s.name}</span>{" "}
              {s.state === "crashed" ? "crashed" : "failed to install"}
            </span>
          </Link>
        ))}
        {offline.slice(0, Math.max(0, 4 - failing.length)).map((n) => (
          <Link
            key={n.id}
            to="/orgs/$orgId/nodes/$nodeId"
            params={{ orgId, nodeId: n.id }}
            className="flex items-start gap-2 hover:underline"
          >
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" />
            <span className="min-w-0">
              <span className="font-medium">{n.name}</span> is offline (last seen{" "}
              {since(n.lastSeenAt)})
            </span>
          </Link>
        ))}
        {failing.length + offline.length > 4 && (
          <p className="text-xs text-muted-foreground">
            And {failing.length + offline.length - 4} more
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function NodesCard({
  orgId,
  nodes,
  metrics,
}: {
  orgId: string;
  nodes: Node[];
  metrics: Map<string, NodeMetrics>;
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>Nodes</CardTitle>
        <CardDescription>The machines your servers run on.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-1">
        {nodes.slice(0, 6).map((n) => {
          const m = metrics.get(n.id);
          return (
            <Link
              key={n.id}
              to="/orgs/$orgId/nodes/$nodeId"
              params={{ orgId, nodeId: n.id }}
              className="-mx-2 flex flex-col gap-2 rounded-md px-2 py-2 text-sm hover:bg-muted/50"
            >
              <span className="flex items-center gap-2">
                <span className="min-w-0 flex-1 truncate font-medium">{n.name}</span>
                <StatusBadge status={nodeStatus(n)} />
              </span>
              {m && (
                <span className="grid grid-cols-2 gap-3 text-xs text-muted-foreground">
                  <span className="flex flex-col gap-1">
                    <span>CPU {formatPercent(m.latest.cpu)}</span>
                    <UsageBar fraction={m.latest.cpu / 100} />
                  </span>
                  <span className="flex flex-col gap-1">
                    <span>
                      Memory {formatBytes(m.latest.memory)} of {formatBytes(m.totals.memory_bytes)}
                    </span>
                    <UsageBar fraction={m.latest.memory / (m.totals.memory_bytes || 1)} />
                  </span>
                </span>
              )}
            </Link>
          );
        })}
        {nodes.length > 6 && (
          <Link
            to="/orgs/$orgId/nodes"
            params={{ orgId }}
            className="pt-1 text-xs text-muted-foreground hover:underline"
          >
            All {nodes.length} nodes
          </Link>
        )}
      </CardContent>
    </Card>
  );
}
