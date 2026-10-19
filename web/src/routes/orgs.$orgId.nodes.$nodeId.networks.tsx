import { createFileRoute } from "@tanstack/react-router";

import { NodeNetworks } from "@/components/node-networks";
import { useIds } from "@/lib/ids";
import { networksCapability } from "@/lib/networks";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/networks")({
  component: Networks,
});

function Networks() {
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  if (!node) return null;
  return (
    <NodeNetworks
      orgId={orgId}
      nodeId={nodeId}
      connected={node.connected}
      supported={node.capabilities.includes(networksCapability)}
    />
  );
}
