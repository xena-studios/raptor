import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { lazy, Suspense, useState } from "react";

import { AppShell } from "@/components/app-shell";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { serverStatus } from "@/lib/servers";
import { requireSession } from "@/lib/session";

const Console = lazy(() => import("@/components/console"));

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId_/servers/$serverId")({
  beforeLoad: ({ location }) => requireSession(location),
  component: ServerPage,
});

function ServerPage() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const nodes = useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 10_000 });
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  const servers = useQuery(
    OrgService.method.listServers,
    { orgId, nodeId },
    { refetchInterval: 5_000 },
  );
  const server = servers.data?.servers.find((s) => s.id === serverId);
  const [error, setError] = useState("");
  const can = (p: string) =>
    !!server && (server.permissions.includes("*") || server.permissions.includes(p));
  const status = server && serverStatus(server, node?.connected ?? false);

  return (
    <AppShell session={session}>
      <p className="mb-1 text-sm text-muted-foreground">
        <Link to="/orgs/$orgId" params={{ orgId }} className="hover:underline">
          {org?.name ?? "Org"}
        </Link>
        {" / "}
        <Link
          to="/orgs/$orgId/nodes/$nodeId"
          params={{ orgId, nodeId }}
          className="hover:underline"
        >
          {node?.name ?? "Node"}
        </Link>
      </p>
      {servers.data && !server ? (
        <p className="text-sm text-muted-foreground">
          This server doesn't exist, or you don't have access to it.
        </p>
      ) : (
        <>
          <div className="mb-1 flex flex-wrap items-center gap-3">
            <h1 className="font-heading text-2xl font-semibold">{server?.name ?? "Server"}</h1>
            {status && <StatusBadge status={status} />}
            <div className="ml-auto flex items-center gap-1">
              {can("power") && (
                <PowerButtons nodeId={nodeId} serverId={serverId} onError={setError} />
              )}
            </div>
          </div>
          <p className="text-sm text-muted-foreground">
            {server?.eggName}
            {server?.ports[0] ? ` · port ${server.ports[0]}` : ""}
          </p>
          {status && <StatusDetail status={status} />}
          {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
          <div className="mt-6">
            {can("console.read") || can("console.write") ? (
              <Suspense
                fallback={<p className="text-sm text-muted-foreground">Loading the console…</p>}
              >
                <Console nodeId={nodeId} serverId={serverId} canWrite={can("console.write")} />
              </Suspense>
            ) : (
              server && (
                <p className="text-sm text-muted-foreground">
                  You don't have access to this server's console.
                </p>
              )
            )}
          </div>
        </>
      )}
    </AppShell>
  );
}
