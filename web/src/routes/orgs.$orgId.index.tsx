import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, Server, SquareTerminal } from "lucide-react";

import { EmptyState, PageHeader } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin } from "@/lib/format";
import { useOrgNodes, useOrgServers } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/")({
  component: Overview,
});

function Overview() {
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const nodes = useOrgNodes(orgId);
  const { servers } = useOrgServers(orgId);
  const nodeList = nodes.data?.nodes ?? [];
  const online = nodeList.filter((n) => n.connected).length;
  const running = servers.filter((s) => s.state === "running").length;
  const admin = isAdmin(org?.role);

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
      <div className="grid gap-4 sm:grid-cols-3">
        <Stat label="Nodes online" value={`${online} / ${nodeList.length}`} />
        <Stat label="Servers running" value={`${running} / ${servers.length}`} />
        <Stat
          label="Servers needing attention"
          value={String(
            servers.filter((s) => ["crashed", "install_failed"].includes(s.state)).length,
          )}
        />
      </div>
      {nodeList.length === 0 ? (
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
        <div className="grid gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Servers</CardTitle>
              <CardDescription>What's running across your nodes.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col divide-y">
              {servers.length === 0 && (
                <p className="text-sm text-muted-foreground">No servers yet.</p>
              )}
              {servers.slice(0, 8).map((s) => (
                <Link
                  key={`${s.node.id}/${s.id}`}
                  to="/orgs/$orgId/servers/$nodeId/$serverId"
                  params={{ orgId, nodeId: s.node.id, serverId: s.id }}
                  className="flex items-center gap-3 py-2 text-sm hover:underline"
                >
                  <Server className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">{s.name}</span>
                  <StatusBadge status={serverStatus(s, s.node.connected)} />
                </Link>
              ))}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Nodes</CardTitle>
              <CardDescription>The machines your servers run on.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col divide-y">
              {nodeList.slice(0, 8).map((n) => (
                <Link
                  key={n.id}
                  to="/orgs/$orgId/nodes/$nodeId"
                  params={{ orgId, nodeId: n.id }}
                  className="flex items-center gap-3 py-2 text-sm hover:underline"
                >
                  <span
                    className={
                      n.connected
                        ? "size-2 rounded-full bg-emerald-500"
                        : "size-2 rounded-full bg-muted-foreground/40"
                    }
                  />
                  <span className="min-w-0 flex-1 truncate">{n.name}</span>
                  <span className="text-xs text-muted-foreground">
                    {n.connected ? "Connected" : "Offline"}
                  </span>
                </Link>
              ))}
            </CardContent>
          </Card>
        </div>
      )}
    </>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl tabular-nums">{value}</CardTitle>
      </CardHeader>
    </Card>
  );
}
