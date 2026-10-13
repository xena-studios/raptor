import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";
import { Database, Layers, Plug, Rocket } from "lucide-react";

import { DetailList } from "@/components/page";
import { StatCard } from "@/components/stats";
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

export const Route = createFileRoute("/admin/overview/infrastructure")({
  component: Infrastructure,
});

// Infrastructure: the Panel itself. Its instances and the nodes each
// holds, its database, sessions, and Wings rollouts. Grafana has the rest
// (latency, errors, the host).
function Infrastructure() {
  const q = useQuery(StaffService.method.getInfrastructure, {}, { refetchInterval: 30_000 });
  if (q.error) return <p className="text-sm text-destructive">{message(q.error)}</p>;
  const d = q.data;
  if (!d) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard
          icon={Layers}
          label="Panel instances"
          value={String(d.instances.length)}
          sub={`Holding ${d.nodesConnected} node connections`}
        />
        <StatCard
          icon={Database}
          label="Database"
          value={formatBytes(Number(d.databaseBytes))}
          sub={`${d.databaseConnections} connections`}
        />
        <StatCard
          icon={Plug}
          label="Sessions"
          value={String(d.liveSessions)}
          sub={`${d.sessionsLastHour} used in the last hour`}
        />
        <StatCard
          icon={Rocket}
          label="This Panel"
          value={d.version.split(" ")[0] ?? d.version}
          sub={d.version}
        />
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Instances</CardTitle>
            <CardDescription>
              Panel processes seen in the last 10 minutes, and the nodes connected to each.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Instance</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead className="text-right">Nodes</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.instances.map((i) => (
                  <TableRow key={i.id}>
                    <TableCell className="font-mono text-xs">{i.id}</TableCell>
                    <TableCell className="text-muted-foreground">{when(i.startedAt)}</TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums">
                      {i.nodes}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Largest tables</CardTitle>
            <CardDescription>Size on disk, with indexes; rows are estimates.</CardDescription>
          </CardHeader>
          <CardContent>
            <DetailList
              rows={d.largestTables.map((t) => [
                t.name,
                <span key={t.name} className="tabular-nums">
                  {formatBytes(Number(t.bytes))}
                  <span className="ml-2 text-muted-foreground">
                    {Number(t.rows).toLocaleString()} rows
                  </span>
                </span>,
              ])}
            />
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Wings rollout</CardTitle>
          <CardDescription>
            The latest, as panel rollout status says it. Started and steered from the server.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <pre className="font-mono text-xs whitespace-pre-wrap text-muted-foreground">
            {d.rollout || "Unknown"}
          </pre>
        </CardContent>
      </Card>
    </div>
  );
}
