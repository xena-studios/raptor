import { useQuery } from "@connectrpc/connect-query";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// useGameTab says whether a server gets the Game tab (#436): its game's
// own settings and version, for games Raptor has them for (Minecraft Java
// and its proxies, for now), to those who can read its files.
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
  // Minecraft Java servers and proxies (the proxies for their version).
  return cfg.egg?.players === "minecraft";
}

// isProxy: Velocity, Waterfall, BungeeCord answer Minecraft's ping but
// have no server.properties.
export function isProxy(cfg: ServerConfig | undefined): boolean {
  const loaders = Object.values(cfg?.egg?.addons ?? {}).flatMap((a) => a.loaders);
  return ["velocity", "bungeecord", "waterfall"].includes(loaders[0] ?? "");
}
