import { createFileRoute } from "@tanstack/react-router";

import { FrameworksPage } from "@/components/cs2-frameworks";
import { useIds } from "@/lib/ids";
import { useServer } from "@/lib/org-data";

// Counter-Strike 2's Metamod:Source and CounterStrikeSharp (#414).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/addons/frameworks")({
  component: function Frameworks() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    return <FrameworksPage orgId={orgId} nodeId={nodeId} serverId={serverId} server={server} />;
  },
});
