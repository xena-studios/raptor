import { createFileRoute } from "@tanstack/react-router";

import { Players } from "@/components/players";
import { RconPlayers } from "@/components/rcon-players";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import { isRconGame } from "@/lib/rcon-players";

// A server's players: a Minecraft server's whitelist, operators, and bans
// (#422), or a Steam game's players over RCON (#423).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/players")({
  component: function PlayersTab() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    const running = server.state === "running" || server.state === "starting";
    if (isRconGame(server.rconPlayers))
      return (
        <RconPlayers
          nodeId={nodeId}
          serverId={serverId}
          game={server.rconPlayers}
          running={running}
          canPlayers={can(server, "players")}
        />
      );
    return (
      <Players
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        running={running}
        canPlayers={can(server, "players")}
        canCommand={can(server, "console.write")}
        canWrite={can(server, "files.write")}
      />
    );
  },
});
