import { useQuery } from "@connectrpc/connect-query";
import { useMemo } from "react";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { addonKinds } from "@/lib/eggs";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// AddonSection is one part of a server's Add-ons tab (#436): an add-on
// kind's page (Plugins, Mods, Datapacks) or the Workshop.
export type AddonSection = { key: string; label: string };

// useAddonSections is what a server's Add-ons tab holds, in order, for
// this user: the egg's add-on kinds (to those who can read its files; a
// kind that needs a node capability only on nodes that have it), then the
// Workshop (to those who change its startup, or read its files when a
// config file lists the items). Empty: no Add-ons tab.
export function useAddonSections(
  orgId: string,
  nodeId: string,
  serverId: string,
  server: Server | undefined,
  capabilities: string[] | undefined,
): { sections: AddonSection[]; loading: boolean } {
  const details = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: can(server, "files.read") || can(server, "startup") },
  );
  const sections = useMemo(() => {
    const cfg = details.data?.configJson
      ? (JSON.parse(details.data.configJson) as ServerConfig)
      : undefined;
    const out: AddonSection[] = [];
    if (can(server, "files.read")) {
      for (const k of Object.keys(cfg?.egg?.addons ?? {})) {
        const meta = addonKinds[k];
        if (!meta) continue;
        const cap = (meta as { capability?: string }).capability;
        if (cap && !capabilities?.includes(cap)) continue;
        out.push({ key: k, label: meta.tab });
      }
    }
    // Bedrock's packs: Mojang's server and Endstone, which runs it.
    const bedrock =
      /^minecraft\/(bedrock|endstone)$/.test(server?.eggSource ?? "") ||
      /^Minecraft: (Bedrock|Endstone)$/.test(server?.eggName ?? "");
    if (bedrock && can(server, "files.read")) out.push({ key: "bedrock", label: "Packs" });
    // A Workshop in a config file (#408) is read with files.read.
    const ws = cfg?.egg?.workshop;
    if (ws && can(server, ws.file ? "files.read" : "startup"))
      out.push({ key: "workshop", label: "Workshop" });
    return out;
  }, [details.data?.configJson, server, capabilities]);
  return { sections, loading: details.isPending && details.fetchStatus !== "idle" };
}
