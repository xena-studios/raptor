import { createFileRoute } from "@tanstack/react-router";

import { Access } from "@/components/server-access";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/access")({
  component: AccessTab,
});

function AccessTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Who has access</CardTitle>
        <CardDescription>
          What each member may do on this server. Admins and owners can always do everything.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Access orgId={orgId} nodeId={nodeId} serverId={serverId} />
      </CardContent>
    </Card>
  );
}
