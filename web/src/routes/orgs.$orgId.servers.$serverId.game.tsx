import { createFileRoute } from "@tanstack/react-router";

import { GameSettings } from "@/components/game-settings";
import { ServerListLook } from "@/components/server-list-look";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

// A server's Game tab (#436): the game's own settings, as a form, and how
// it looks in players' server list.
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/game")({
  component: function Game() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    return (
      <div className="flex flex-col gap-6">
        <ServerListLook
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          canWrite={can(server, "files.write")}
        />
        <GameSettings
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          canWrite={can(server, "files.write")}
        />
      </div>
    );
  },
});
