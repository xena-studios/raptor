import { useQuery } from "@connectrpc/connect-query";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// useGameTab says whether a server gets the Game tab (#436): its game's
// own settings, for games Raptor has a form for (Minecraft Java, for now),
// to those who can read its files.
export function useGameTab(
  orgId: string,
  nodeId: string,
  serverId: string,
  server: Server | undefined,
) {
  const details = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: can(server, "files.read") },
  );
  if (!can(server, "files.read") || !details.data?.configJson) return false;
  const cfg = JSON.parse(details.data.configJson) as ServerConfig;
  // Proxies answer Minecraft's ping too, but have no server.properties.
  const loaders = Object.values(cfg.egg?.addons ?? {}).flatMap((a) => a.loaders);
  const proxy = ["velocity", "bungeecord", "waterfall"].includes(loaders[0] ?? "");
  return cfg.egg?.players === "minecraft" && !proxy;
}
