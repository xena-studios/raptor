import { createFileRoute } from "@tanstack/react-router";

import { Worlds } from "@/components/worlds";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

// A Minecraft server's worlds and game rules (#426).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/worlds")({
  component: function WorldsTab() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    return (
      <Worlds
        nodeId={nodeId}
        serverId={serverId}
        running={server.state === "running" || server.state === "starting"}
        canWrite={can(server, "files.write")}
        canCommand={can(server, "console.write")}
      />
    );
  },
});
