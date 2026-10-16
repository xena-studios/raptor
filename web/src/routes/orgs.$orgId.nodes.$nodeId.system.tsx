import { createFileRoute } from "@tanstack/react-router";

import { NodeSystem } from "@/components/node-system";
import { useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/system")({
  component: System,
});

function System() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  return <NodeSystem orgId={orgId} node={node} userId={session.user?.id ?? ""} />;
}
