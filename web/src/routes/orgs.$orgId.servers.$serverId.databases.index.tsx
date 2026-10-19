import { createFileRoute } from "@tanstack/react-router";

import { ServerDatabases } from "@/components/server-databases";
import { databasesCapability } from "@/lib/databases";
import { dbBrowserCapability, dbTransfersCapability } from "@/lib/db-browser";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/databases/")({
  component: DatabasesTab,
});

function DatabasesTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server || !node) return null;
  return (
    <ServerDatabases
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      userId={session.user?.id ?? ""}
      canWrite={can(server, "databases.write")}
      admin={can(server, "*")}
      supported={node.capabilities.includes(databasesCapability)}
      connected={node.connected}
      browsable={node.capabilities.includes(dbBrowserCapability)}
      transfers={node.capabilities.includes(dbTransfersCapability)}
    />
  );
}
