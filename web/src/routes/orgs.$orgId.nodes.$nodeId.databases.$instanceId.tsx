import { createFileRoute } from "@tanstack/react-router";

import { InstanceBrowser } from "@/components/db-instance";
import { dbUsageCapability } from "@/lib/databases";
import { dbQueryStreamCapability, dbTransfersCapability } from "@/lib/db-browser";
import { useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/databases/$instanceId")({
  component: Instance,
});

function Instance() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const { instanceId } = Route.useParams();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  if (!node.connected) {
    return (
      <p className="text-sm text-muted-foreground">
        The node is offline, so its databases can't be read until it's back.
      </p>
    );
  }
  return (
    <InstanceBrowser
      orgId={orgId}
      nodeId={nodeId}
      instanceId={instanceId}
      userId={session.user?.id ?? ""}
      transfers={node.capabilities.includes(dbTransfersCapability)}
      stream={node.capabilities.includes(dbQueryStreamCapability)}
      usage={node.capabilities.includes(dbUsageCapability)}
    />
  );
}
