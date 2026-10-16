import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { IdsCard } from "@/components/ids-card";
import { NodeName, NodeRemove, NodeSFTP } from "@/components/node-settings";
import { PairKey } from "@/components/pair-key";
import { SupportSettings } from "@/components/support-access";
import { TrustedKeys } from "@/components/trusted-keys";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/settings")({
  component: Settings,
});

function Settings() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  const servers = useQuery(OrgService.method.listServers, { orgId, nodeId });
  const userId = session.user?.id ?? "";
  return (
    <div className="flex flex-col gap-6">
      {node && <NodeName key={node.name} orgId={orgId} nodeId={nodeId} name={node.name} />}
      {node && <NodeSFTP orgId={orgId} node={node} />}
      {node && <SupportSettings nodeId={nodeId} connected={node.connected} />}
      <TrustedKeys
        orgId={orgId}
        nodeId={nodeId}
        userId={userId}
        servers={servers.data?.servers ?? []}
      />
      <PairKey nodeId={nodeId} userId={userId} />
      {node && <IdsCard id={nodeId} what="node" />}
      {node && <NodeRemove orgId={orgId} nodeId={nodeId} name={node.name} />}
    </div>
  );
}
