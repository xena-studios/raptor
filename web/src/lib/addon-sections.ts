import { useQuery } from "@connectrpc/connect-query";
import { useMemo } from "react";

import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { hasFrameworks } from "@/lib/cs2-frameworks";
import { addonKinds } from "@/lib/eggs";
import { can } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// AddonSection is one part of a server's Add-ons tab (#436): an add-on
// kind's page (Plugins, Mods, Datapacks), the Workshop, or CS2's
// Frameworks.
export type AddonSection = { key: string; label: string };

// useAddonSections is what a server's Add-ons tab holds, in order, for
// this user: the egg's add-on kinds (to those who can read its files; a
// kind that needs a node capability only on nodes that have it), then the
// Workshop and CS2's plugin frameworks (to those who change its startup).
// Empty: no Add-ons tab.
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
    if (cfg?.egg?.workshop && can(server, "startup"))
      out.push({ key: "workshop", label: "Workshop" });
    // Metamod:Source and CounterStrikeSharp: eggs with their variables.
    if (hasFrameworks((cfg?.egg?.variables ?? []).map((v) => v.env)) && can(server, "startup"))
      out.push({ key: "frameworks", label: "Frameworks" });
    return out;
  }, [details.data?.configJson, server, capabilities]);
  return { sections, loading: details.isPending && details.fetchStatus !== "idle" };
}
