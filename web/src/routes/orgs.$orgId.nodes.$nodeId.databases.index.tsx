import { createFileRoute } from "@tanstack/react-router";

import { NodeDatabases } from "@/components/node-databases";
import {
  databasesCapability,
  dbExternalCapability,
  dbRedisExternalCapability,
  dbSettingsCapability,
  dbSharedCapability,
  dbUsageCapability,
  dbVersionsCapability,
} from "@/lib/databases";
import { dbInstanceBrowserCapability } from "@/lib/db-browser";
import { useIds } from "@/lib/ids";
import { networksCapability } from "@/lib/networks";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/databases/")({
  component: Databases,
});

function Databases() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  return (
    <NodeDatabases
      orgId={orgId}
      nodeId={nodeId}
      userId={session.user?.id ?? ""}
      connected={node.connected}
      supported={node.capabilities.includes(databasesCapability)}
      networksSupported={node.capabilities.includes(networksCapability)}
      versionsSupported={node.capabilities.includes(dbVersionsCapability)}
      externalSupported={node.capabilities.includes(dbExternalCapability)}
      settingsSupported={node.capabilities.includes(dbSettingsCapability)}
      browserSupported={node.capabilities.includes(dbInstanceBrowserCapability)}
      redisExternalSupported={node.capabilities.includes(dbRedisExternalCapability)}
      usageSupported={node.capabilities.includes(dbUsageCapability)}
      sharedSupported={node.capabilities.includes(dbSharedCapability)}
    />
  );
}
