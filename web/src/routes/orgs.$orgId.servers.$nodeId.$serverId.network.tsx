import { createFileRoute } from "@tanstack/react-router";

import { ConnectionTest } from "@/components/connection-test";
import { DetailList } from "@/components/page";
import { ServerSettings } from "@/components/server-settings";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/network")({
  component: NetworkTab,
});

function NetworkTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = Route.useParams();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  const host = node ? `n-${node.shortId}.raptornodes.net` : "";
  return (
    <div className="flex flex-col gap-6">
      <ConnectionTest orgId={orgId} nodeId={nodeId} serverId={serverId} host={host} />
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Addresses</CardTitle>
            <CardDescription>
              Where players connect. The first port is the one the game listens on.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {server.ports.length === 0 ? (
              <p className="text-sm text-muted-foreground">This server has no ports.</p>
            ) : (
              <DetailList
                rows={server.ports.map((p, i) => [
                  i === 0 ? "Primary" : `Port ${i + 1}`,
                  <code key={p} className="text-xs">
                    {host}:{p}
                  </code>,
                ])}
              />
            )}
          </CardContent>
        </Card>
        {can(server, "startup") && (
          <ServerSettings
            section="network"
            orgId={orgId}
            nodeId={nodeId}
            serverId={serverId}
            userId={session.user?.id ?? ""}
            admin={can(server, "*")}
            canReinstall={false}
            stopped={false}
          />
        )}
      </div>
    </div>
  );
}
