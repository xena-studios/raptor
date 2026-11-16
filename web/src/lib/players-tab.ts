import { useQuery } from "@connectrpc/connect-query";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// usePlayersTab says whether a server gets the Players tab (#422, #436):
// Minecraft Java servers (not proxies, which keep no player lists), for
// those who can read their files or send console commands.
export function usePlayersTab(
  orgId: string,
  nodeId: string,
  serverId: string,
  server: Server | undefined,
): boolean {
  const allowed = can(server, "files.read") || can(server, "console.write");
  const details = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: allowed },
  );
  if (!allowed || !details.data?.configJson) return false;
  const cfg = JSON.parse(details.data.configJson) as ServerConfig;
  const loaders = Object.values(cfg.egg?.addons ?? {}).flatMap((a) => a.loaders);
  const proxy = ["velocity", "bungeecord", "waterfall"].includes(loaders[0] ?? "");
  return cfg.egg?.players === "minecraft" && !proxy;
}
