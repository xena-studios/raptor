import { createFileRoute } from "@tanstack/react-router";
import { lazy, Suspense } from "react";

import { DetailList } from "@/components/page";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { can, useServer } from "@/lib/org-data";

const Console = lazy(() => import("@/components/console"));

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/")({
  component: ConsoleTab,
});

function ConsoleTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  const address =
    node && server.ports[0] ? `n-${node.shortId}.raptornodes.net:${server.ports[0]}` : "—";

  return (
    <div className="grid items-start gap-6 lg:grid-cols-3">
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Console</CardTitle>
          <CardDescription>Live output from your server.</CardDescription>
        </CardHeader>
        <CardContent>
          {can(server, "console.read") || can(server, "console.write") ? (
            <Suspense
              fallback={<p className="text-sm text-muted-foreground">Loading the console…</p>}
            >
              <Console
                nodeId={nodeId}
                serverId={serverId}
                canWrite={can(server, "console.write")}
              />
            </Suspense>
          ) : (
            <p className="text-sm text-muted-foreground">
              You don't have access to this server's console.
            </p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
          <CardDescription>Where players reach this server.</CardDescription>
        </CardHeader>
        <CardContent>
          <DetailList
            rows={[
              [
                "Connect",
                <span key="c" className="font-mono text-xs">
                  {address}
                </span>,
              ],
              ["Game", server.eggName || "—"],
              ["Node", node?.name ?? "—"],
              ["SFTP ID", <code key="s">{server.id.slice(-8)}</code>],
            ]}
          />
        </CardContent>
      </Card>
    </div>
  );
}
