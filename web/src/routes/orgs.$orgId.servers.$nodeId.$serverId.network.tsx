import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { ServerAddress } from "@/components/address";
import { ConnectionTest } from "@/components/connection-test";
import { DetailList } from "@/components/page";
import { ShownAddress } from "@/components/server-address";
import { ServerSettings } from "@/components/server-settings";
import { SubdomainCard } from "@/components/subdomain";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { nodeHostname } from "@/lib/addresses";
import { can, useServer } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/network")({
  component: NetworkTab,
});

function NetworkTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = Route.useParams();
  const { node, server } = useServer(orgId, nodeId, serverId);
  const details = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  if (!server) return null;
  const cfg = details.data?.configJson
    ? (JSON.parse(details.data.configJson) as ServerConfig)
    : undefined;
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
        <SubdomainCard
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          port={server.ports[0]}
          minecraft={cfg?.egg?.players === "minecraft"}
          admin={can(server, "*")}
        />
      </div>
    </div>
  );
}
