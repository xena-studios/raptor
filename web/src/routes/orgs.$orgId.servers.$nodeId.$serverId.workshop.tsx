import { createFileRoute } from "@tanstack/react-router";

import { WorkshopPage } from "@/components/workshop";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

// A server's Steam Workshop items (docs/EGGS.md#steam-workshop).
export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/workshop")({
  component: function Workshop() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    return (
      <WorkshopPage
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        canWrite={can(server, "startup")}
      />
    );
  },
});
