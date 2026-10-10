import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";

import { Health, useDestinations, where } from "@/components/backup-destinations";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { Node } from "@/gen/raptor/panel/v1/org_pb";
import { typeNames } from "@/lib/backup-destinations";
import { message } from "@/lib/errors";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/settings/backups")({
  component: Backups,
});

// The org's backup destinations, node by node, with how each is doing.
// Destinations live on each node (with their credentials, which never
// leave it), so they're added and changed on the node's Backups tab.
function Backups() {
  const { orgId } = Route.useParams();
  const nodes = useOrgNodes(orgId).data?.nodes ?? [];
  const offline = nodes.filter((n) => !n.connected);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Backups</CardTitle>
        <CardDescription>
          Where each node keeps its servers' backups, and how each place is doing. Credentials stay
          on the node they're for, so destinations are added on each node's Backups tab.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {nodes.length === 0 && <p className="text-sm text-muted-foreground">No nodes yet.</p>}
        {nodes
          .filter((n) => n.connected)
          .map((n) => (
            <NodeRow key={n.id} orgId={orgId} node={n} />
          ))}
        {offline.length > 0 && (
          <p className="text-sm text-muted-foreground">
            Offline, so not shown: {offline.map((n) => n.name).join(", ")}. Their backups keep
            running on their own.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function NodeRow({ orgId, node }: { orgId: string; node: Node }) {
  const list = useDestinations(node.id, node.connected);
  return (
    <section className="rounded-lg border">
      <Link
        to="/orgs/$orgId/nodes/$nodeId/backups"
        params={{ orgId, nodeId: node.id }}
        className="flex items-center gap-2 border-b px-3 py-2 text-sm font-medium hover:bg-muted/40"
      >
        {node.name}
        {!node.connected && <Badge variant="outline">Offline</Badge>}
        <ChevronRight className="ml-auto size-4 text-muted-foreground" />
      </Link>
      {!node.connected ? (
        <p className="px-3 py-2 text-sm text-muted-foreground">
          Offline: its backups keep running on their own.
        </p>
      ) : list.error ? (
        <p className="px-3 py-2 text-sm text-destructive">{message(list.error)}</p>
      ) : !list.data ? (
        <p className="px-3 py-2 text-sm text-muted-foreground">Asking the node…</p>
      ) : (
        <ul className="divide-y">
          {list.data.destinations.map((d) => (
            <li
              key={d.id}
              className="grid gap-1 px-3 py-2 sm:grid-cols-[14rem_1fr] sm:items-center"
            >
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-sm">{d.name}</span>
                <span className="truncate text-xs text-muted-foreground">
                  {typeNames[d.type]} · {where(d)}
                </span>
              </span>
              <Health d={d} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
