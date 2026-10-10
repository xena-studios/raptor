import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";

import { Health, useDestinations, where } from "@/components/backup-destinations";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { typeNames } from "@/lib/backup-destinations";
import { message } from "@/lib/errors";
import { formatBytes } from "@/lib/metrics";
import { useOrgNodes } from "@/lib/org-data";
import { cn } from "@/lib/utils";

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
    <div className="flex flex-col gap-6">
      <StorageUsage orgId={orgId} />
      <Card>
        <CardHeader>
          <CardTitle>Backups</CardTitle>
          <CardDescription>
            Where each node keeps its servers' backups, and how each place is doing. Credentials
            stay on the node they're for, so destinations are added on each node's Backups tab.
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
    </div>
  );
}

const dollars = (cents: number) =>
  cents % 100 === 0 ? `$${cents / 100}` : `$${(cents / 100).toFixed(2)}`;

// StorageUsage is the org's Raptor Backup Storage: what's stored, what's
// included, and what a month would cost.
function StorageUsage({ orgId }: { orgId: string }) {
  const q = useQuery(OrgService.method.getBackupStorage, { orgId });
  const d = q.data;
  if (!d?.available) return null;
  const used = Number(d.usedBytes);
  const included = Number(d.includedBytes);
  const share = included > 0 ? Math.min(1, used / included) : used > 0 ? 1 : 0;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Raptor Backup Storage</CardTitle>
        <CardDescription>
          {formatBytes(10 * 2 ** 30)} included with every node, then {dollars(Number(d.centsPerTb))}{" "}
          per TB a month, billed by the GB.{" "}
          {d.nodeIds.length
            ? `On for ${d.nodeIds.length} ${d.nodeIds.length === 1 ? "node" : "nodes"}.`
            : "Not on for any node yet: turn it on from a node's Backups tab."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2 text-sm">
          <span>
            <span className="text-lg font-semibold tabular-nums">{formatBytes(used)}</span>{" "}
            <span className="text-muted-foreground">of {formatBytes(included)} included</span>
          </span>
          <span className="text-muted-foreground">
            {Number(d.estimateCents) > 0
              ? `About ${dollars(Number(d.estimateCents))} this month`
              : "Nothing to pay at this size"}
          </span>
        </div>
        <div className="h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className={cn("h-full rounded-full", share >= 1 ? "bg-amber-500" : "bg-primary")}
            style={{ width: `${share * 100}%` }}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          {d.measuredAt
            ? `Measured ${timestampDate(d.measuredAt).toLocaleString()}; measured once a day.`
            : "Measured once a day; nothing measured yet."}
          {used > Number(d.softCapBytes) &&
            " Your org stores more than we expected during the beta: get in touch and we'll raise the limit."}
        </p>
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
