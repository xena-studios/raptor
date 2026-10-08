import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, SquareTerminal } from "lucide-react";
import { useState } from "react";

import { EmptyState, ListToolbar, type ListView, PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin, when } from "@/lib/format";
import { formatMemory, useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/")({
  component: Nodes,
});

function Nodes() {
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const nodes = useOrgNodes(orgId);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<ListView>("grid");
  const q = query.trim().toLowerCase();
  const all = nodes.data?.nodes ?? [];
  const list = all.filter((n) => !q || n.name.toLowerCase().includes(q) || n.shortId.includes(q));
  const connect = admin && (
    <Link to="/orgs/$orgId/nodes/new" params={{ orgId }} className={buttonVariants({ size: "sm" })}>
      <Plus /> Connect node
    </Link>
  );

  return (
    <>
      <PageHeader
        title="Nodes"
        description="The machines running your servers."
        actions={connect}
      />
      {nodes.data && all.length === 0 ? (
        <EmptyState
          icon={SquareTerminal}
          title="No nodes yet"
          description="Connect a machine to start running servers on it."
          action={connect}
        />
      ) : (
        <>
          <ListToolbar
            noun="node"
            count={list.length}
            query={query}
            onQuery={setQuery}
            view={view}
            onView={setView}
          />
          {view === "grid" ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {list.map((n) => (
                <NodeCard key={n.id} orgId={orgId} node={n} />
              ))}
            </div>
          ) : (
            <Card className="py-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Hostname</TableHead>
                    <TableHead>Machine</TableHead>
                    <TableHead>Wings</TableHead>
                    <TableHead className="text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((n) => (
                    <TableRow key={n.id}>
                      <TableCell>
                        <Link
                          to="/orgs/$orgId/nodes/$nodeId"
                          params={{ orgId, nodeId: n.id }}
                          className="font-medium hover:underline"
                        >
                          {n.name}
                        </Link>
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        n-{n.shortId}.raptornodes.net
                      </TableCell>
                      <TableCell>{machine(n)}</TableCell>
                      <TableCell>{n.wingsVersion || "—"}</TableCell>
                      <TableCell className="text-right">
                        <Status node={n} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </Card>
          )}
        </>
      )}
    </>
  );
}

function machine(n: Node): string {
  return (
    [n.arch, n.cpus ? `${n.cpus} cores` : "", n.memoryBytes ? formatMemory(n.memoryBytes) : ""]
      .filter(Boolean)
      .join(" · ") || "—"
  );
}

function Status({ node }: { node: Node }) {
  return (
    <Badge variant={node.connected ? "default" : "outline"}>
      {node.connected ? "Connected" : "Offline"}
    </Badge>
  );
}

function NodeCard({ orgId, node }: { orgId: string; node: Node }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="truncate">
          <Link
            to="/orgs/$orgId/nodes/$nodeId"
            params={{ orgId, nodeId: node.id }}
            className="hover:underline"
          >
            {node.name}
          </Link>
        </CardTitle>
        <CardDescription className="truncate font-mono text-xs">
          n-{node.shortId}.raptornodes.net
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 text-sm">
        <div className="flex justify-between gap-2">
          <span className="text-muted-foreground">Machine</span>
          <span className="truncate">{machine(node)}</span>
        </div>
        <div className="flex justify-between gap-2">
          <span className="text-muted-foreground">Wings</span>
          <span>{node.wingsVersion || "—"}</span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-muted-foreground">
            {node.connected ? "Connected" : `Last seen ${when(node.lastSeenAt)}`}
          </span>
          <Status node={node} />
        </div>
      </CardContent>
    </Card>
  );
}
