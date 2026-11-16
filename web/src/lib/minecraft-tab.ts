import { useQuery } from "@connectrpc/connect-query";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// useMinecraftServer says whether a server is a Minecraft Java server (not
// a proxy: those have no worlds or player lists), for its own tabs, read
// by those who can read its files.
export function useMinecraftServer(
  orgId: string,
  nodeId: string,
  serverId: string,
  server: Server | undefined,
): boolean {
  const allowed = can(server, "files.read");
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
