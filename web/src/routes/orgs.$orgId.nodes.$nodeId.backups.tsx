import { createFileRoute } from "@tanstack/react-router";

import { NodeDestinations } from "@/components/backup-destinations";
import { Card, CardContent } from "@/components/ui/card";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/backups")({
  component: Backups,
});

// A node's backup destinations (admins).
function Backups() {
  const { orgId, nodeId } = Route.useParams();
  const session = Route.useRouteContext();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
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
    <NodeDestinations
      orgId={orgId}
      nodeId={nodeId}
      userId={session.user?.id ?? ""}
      nodeName={node?.name ?? "This node"}
    />
  );
}
