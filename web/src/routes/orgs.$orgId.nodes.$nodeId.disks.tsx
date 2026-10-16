import { createFileRoute } from "@tanstack/react-router";

import { NodeDisks } from "@/components/node-disks";
import { useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/disks")({
  component: Disks,
});

function Disks() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  return <NodeDisks nodeId={nodeId} userId={session.user?.id ?? ""} connected={node.connected} />;
}
