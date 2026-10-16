import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, SquareTerminal } from "lucide-react";
import { useState } from "react";

import { Hidden } from "@/components/address";
import { EntityCard, LinkRow } from "@/components/entity-list";
import { EmptyState, ListToolbar, type ListView, PageHeader } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { nodeHostname } from "@/lib/addresses";
import { isAdmin, since } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { formatMemory, useOrgNodes, useOrgServers } from "@/lib/org-data";
import { nodeStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/nodes/")({
  component: Nodes,
});

function Nodes() {
  const { orgId } = useIds();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const nodes = useOrgNodes(orgId);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<ListView>("grid");
  const q = query.trim().toLowerCase();
  const all = nodes.data?.nodes ?? [];
  const list = all.filter((n) => !q || n.name.toLowerCase().includes(q));
  const { servers } = useOrgServers(orgId);
  const running = (nodeId: string) => {
    const on = servers.filter((s) => s.node.id === nodeId);
    return `${on.filter((s) => s.state === "running").length} running of ${on.length}`;
  };
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
                <EntityCard
                  key={n.id}
                  link={{ to: "/orgs/$orgId/nodes/$nodeId", params: { orgId, nodeId: n.id } }}
                  title={n.name}
                  status={nodeStatus(n)}
                  subtitle={machine(n)}
                  rows={[
                    ["Address", <NodeAddress key="a" node={n} />],
                    ["Servers", running(n.id)],
                    [
                      n.connected ? "Wings" : "Last seen",
                      n.connected ? n.wingsVersion || "—" : since(n.lastSeenAt),
                    ],
                  ]}
                />
              ))}
            </div>
          ) : (
            <Card className="py-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Address</TableHead>
                    <TableHead className="hidden md:table-cell">Machine</TableHead>
                    <TableHead className="hidden sm:table-cell">Servers</TableHead>
                    <TableHead className="text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((n) => (
                    <LinkRow
                      key={n.id}
                      link={{ to: "/orgs/$orgId/nodes/$nodeId", params: { orgId, nodeId: n.id } }}
                    >
                      <TableCell>
                        <Link
                          to="/orgs/$orgId/nodes/$nodeId"
                          params={{ orgId, nodeId: n.id }}
                          className="font-medium hover:underline"
                        >
                          {n.name}
                        </Link>
                      </TableCell>
                      <TableCell>
                        <NodeAddress node={n} />
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground md:table-cell">
                        {machine(n)}
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">{running(n.id)}</TableCell>
                      <TableCell className="text-right">
                        <StatusBadge status={nodeStatus(n)} />
                      </TableCell>
                    </LinkRow>
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

// NodeAddress is the node's own hostname, hidden.
function NodeAddress({ node }: { node: Node }) {
  return <Hidden value={nodeHostname(node)} label="hostname" />;
}
