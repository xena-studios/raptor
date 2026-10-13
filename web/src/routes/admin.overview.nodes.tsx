import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { AlertTriangle, Cpu, MemoryStick, Server, SquareTerminal } from "lucide-react";
import { useState } from "react";

import { Breakdown, DaysPicker, stateNames } from "@/components/admin/insights";
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
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { formatBytes } from "@/lib/metrics";

export const Route = createFileRoute("/admin/overview/nodes")({
  component: NodeInsights,
});

const count = (v: number) => String(Math.round(v));

// Nodes: what's linked and connected, on what, running what, and what's
// wrong.
function NodeInsights() {
  const [days, setDays] = useState(30);
  const q = useQuery(StaffService.method.getNodeInsights, { days }, { refetchInterval: 60_000 });
  if (q.error) return <p className="text-sm text-destructive">{message(q.error)}</p>;
  const d = q.data;
  if (!d) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const to = Date.now();
  const from = to - days * 86400_000;
  const hist = d.history.map((p) => ({ ...p, t: p.at ? timestampDate(p.at).getTime() : 0 }));
  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard
          icon={SquareTerminal}
          label="Nodes connected"
          value={`${d.connected} / ${d.nodes}`}
          sub={`${d.linked30d} linked in 30 days · ${d.removed} removed`}
          fraction={d.nodes ? d.connected / d.nodes : 0}
        />
        <StatCard icon={Cpu} label="Cores" value={String(d.cpus)} sub="Across every linked node" />
        <StatCard
          icon={MemoryStick}
          label="Memory"
          value={formatBytes(Number(d.memoryBytes))}
          sub="Across every linked node"
        />
        <StatCard
          icon={Server}
          label="Servers"
          value={String(d.servers)}
          sub={d.nodes ? `${(d.servers / d.nodes).toFixed(1)} a node on average` : "No nodes yet"}
        />
      </div>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Over time</CardTitle>
            <CardDescription>Counted every hour.</CardDescription>
          </div>
          <DaysPicker value={days} onChange={setDays} />
        </CardHeader>
        <CardContent className="grid gap-8 md:grid-cols-2">
          <Graph title="Nodes">
            <TimeChart
              data={hist.map((p) => ({ t: p.t, values: [p.nodes, p.nodesConnected] }))}
              series={[
                { name: "Linked", color: "var(--series-2)" },
                { name: "Connected", color: "var(--series-1)" },
              ]}
              format={count}
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
              format={count}
              minMax={4}
              from={from}
              to={to}
              gap={3 * 3600_000}
              dots
            />
          </Graph>
        </CardContent>
      </Card>
      <div className="grid items-start gap-6 md:grid-cols-2 lg:grid-cols-3">
        <Breakdown title="Operating systems" buckets={d.oses} />
        <Breakdown title="Wings versions" buckets={d.wingsVersions} />
        <Breakdown title="Architectures" buckets={d.archs} />
        <Breakdown title="Servers per node" buckets={d.serversPerNode} />
        <Breakdown title="Server states" buckets={d.serverStates} names={stateNames} />
        <Breakdown title="Most deployed games" buckets={d.topEggs} />
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle className="size-4 text-warn" /> Offline nodes
            </CardTitle>
            <CardDescription>Gone for over an hour.</CardDescription>
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
                    <TableHead>Wings</TableHead>
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
                      <TableCell className="text-muted-foreground">
                        {n.wingsVersion || "—"}
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
            <CardDescription>Crashed, or failed to install.</CardDescription>
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
