import { createFileRoute } from "@tanstack/react-router";

import { ServerAddress } from "@/components/address";
import { ConnectionTest } from "@/components/connection-test";
import { DetailList } from "@/components/page";
import { ShownAddress } from "@/components/server-address";
import { ServerSettings } from "@/components/server-settings";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { nodeHostname } from "@/lib/addresses";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/network")({
  component: NetworkTab,
});

function NetworkTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  const host = node ? nodeHostname(node) : "";
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
                  node ? <ServerAddress key={p} node={node} server={server} port={p} /> : "—",
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
        {can(server, "*") && (
          <ShownAddress
            key={server.displayAddress}
            orgId={orgId}
            nodeId={nodeId}
            serverId={serverId}
            current={server.displayAddress}
          />
        )}
      </div>
    </div>
  );
}
