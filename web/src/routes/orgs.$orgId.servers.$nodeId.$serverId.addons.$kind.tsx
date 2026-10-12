import { createFileRoute } from "@tanstack/react-router";

import { AddonsPage } from "@/components/addons";
import { can, useServer } from "@/lib/org-data";

// A server's plugins or mods (docs/EGGS.md#add-ons).
export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/addons/$kind")({
  component: function Addons() {
    const { orgId, nodeId, serverId, kind } = Route.useParams();
    const { server } = useServer(orgId, nodeId, serverId);
    return (
      <AddonsPage
        key={kind}
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        kind={kind}
        canWrite={can(server, "files.write")}
      />
    );
  },
});
