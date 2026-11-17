import type { Server } from "@/gen/raptor/panel/v1/org_pb";
import { can } from "@/lib/org-data";

// usePlayersTab says whether a server gets the Players tab (#422, #423,
// #436): Minecraft Java servers (not proxies, which keep no player lists;
// the Panel's player_lists), for those with the players permission, or
// who can read their files or send console commands; and games with RCON
// player commands (rcon_players), for those with the players permission.
export function usePlayersTab(
  _orgId: string,
  _nodeId: string,
  _serverId: string,
  server: Server | undefined,
): boolean {
  if (server?.rconPlayers) return can(server, "players");
  const allowed =
    can(server, "players") || can(server, "files.read") || can(server, "console.write");
  return allowed && !!server?.playerLists;
}
