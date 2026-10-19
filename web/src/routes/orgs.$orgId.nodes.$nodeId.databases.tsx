import { createFileRoute } from "@tanstack/react-router";

import { NodeDatabases } from "@/components/node-databases";
import { databasesCapability, dbVersionsCapability } from "@/lib/databases";
import { useIds } from "@/lib/ids";
import { networksCapability } from "@/lib/networks";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/databases")({
  component: Databases,
});

function Databases() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  return (
    <NodeDatabases
      nodeId={nodeId}
      userId={session.user?.id ?? ""}
      connected={node.connected}
      supported={node.capabilities.includes(databasesCapability)}
      networksSupported={node.capabilities.includes(networksCapability)}
      versionsSupported={node.capabilities.includes(dbVersionsCapability)}
    />
  );
}
