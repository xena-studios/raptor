import { createFileRoute } from "@tanstack/react-router";

import { Players } from "@/components/players";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

// A Minecraft server's players: whitelist, operators, and bans (#422).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/players")({
  component: function PlayersTab() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    return (
      <Players
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        running={server.state === "running" || server.state === "starting"}
        canCommand={can(server, "console.write")}
        canWrite={can(server, "files.write")}
      />
    );
  },
});
