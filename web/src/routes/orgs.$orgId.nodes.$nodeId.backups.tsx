import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { BackupKey, NodeDestinations, RecoveredBackups } from "@/components/backup-destinations";
import { Card, CardContent } from "@/components/ui/card";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/backups")({
  component: Backups,
});

// A node's backup destinations (admins).
function Backups() {
  const { orgId, nodeId } = Route.useParams();
  const session = Route.useRouteContext();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const owner = orgs.data?.orgs.find((o) => o.id === orgId)?.role === Role.OWNER;
  if (node && !node.connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          {node.name} is offline. Its backups keep running on their own; its destinations can be
          changed when it's back.
        </CardContent>
      </Card>
    );
  }
  return (
    <div className="flex flex-col gap-6">
      <NodeDestinations
        owner={owner}
        orgId={orgId}
        nodeId={nodeId}
        userId={session.user?.id ?? ""}
        nodeName={node?.name ?? "This node"}
      />
      <RecoveredBackups orgId={orgId} nodeId={nodeId} userId={session.user?.id ?? ""} />
      <BackupKey
        orgId={orgId}
        nodeId={nodeId}
        userId={session.user?.id ?? ""}
        nodeName={node?.name ?? "node"}
      />
    </div>
  );
}
