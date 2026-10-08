import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Server } from "lucide-react";

import { DetailList, EmptyState } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
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
import { when } from "@/lib/format";
import { formatMemory, useOrgNodes } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/")({
  component: NodeOverview,
});

function NodeOverview() {
  const { orgId, nodeId } = Route.useParams();
  const nodes = useOrgNodes(orgId);
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  const servers = useQuery(
    OrgService.method.listServers,
    { orgId, nodeId },
    { refetchInterval: 5_000 },
  );
  const list = servers.data?.servers ?? [];

  return (
    <div className="grid items-start gap-6 lg:grid-cols-3">
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Servers</CardTitle>
          <CardDescription>What this node runs.</CardDescription>
        </CardHeader>
        <CardContent>
          {servers.data && list.length === 0 ? (
            <EmptyState icon={Server} title="No servers on this node" />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Server</TableHead>
                  <TableHead>Game</TableHead>
                  <TableHead>Port</TableHead>
                  <TableHead className="text-right">Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell>
                      <Link
                        to="/orgs/$orgId/servers/$nodeId/$serverId"
                        params={{ orgId, nodeId, serverId: s.id }}
                        className="font-medium hover:underline"
                      >
                        {s.name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{s.eggName || "—"}</TableCell>
                    <TableCell className="tabular-nums">{s.ports[0] ?? "—"}</TableCell>
                    <TableCell className="text-right">
                      <StatusBadge status={serverStatus(s, node?.connected ?? false)} />
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
          <CardTitle>Details</CardTitle>
          <CardDescription>The machine, as it reported itself.</CardDescription>
        </CardHeader>
        <CardContent>
          {node && (
            <DetailList
              rows={[
                [
                  "Hostname",
                  <span key="h" className="font-mono text-xs">
                    n-{node.shortId}.raptornodes.net
                  </span>,
                ],
                ["Architecture", node.arch || "—"],
                ["Cores", node.cpus ? String(node.cpus) : "—"],
                ["Memory", formatMemory(node.memoryBytes)],
                ["Wings", node.wingsVersion || "—"],
                ["Last seen", node.connected ? "Now" : when(node.lastSeenAt)],
                ["Linked", when(node.createdAt)],
              ]}
            />
          )}
        </CardContent>
      </Card>
    </div>
  );
}
