import { createFileRoute, Navigate } from "@tanstack/react-router";

import { AddonsNone } from "@/components/addons-none";
import { useAddonSections } from "@/lib/addon-sections";
import { useIds } from "@/lib/ids";
import { useServer } from "@/lib/org-data";

// The Add-ons tab opens its first section.
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/addons/")({
  component: function AddonsIndex() {
    const { orgId, nodeId, serverId } = useIds();
    const { node, server } = useServer(orgId, nodeId, serverId);
    const { sections, loading } = useAddonSections(
      orgId,
      nodeId,
      serverId,
      server,
      node?.capabilities,
    );
    const first = sections[0];
    if (!first) {
      return loading || !server ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <AddonsNone />
      );
    }
    const params = { orgId, serverId };
    if (first.key === "bedrock") {
      return (
        <Navigate to="/orgs/$orgId/servers/$serverId/addons/bedrock" params={params} replace />
      );
    }
    if (first.key === "frameworks") {
      return (
        <Navigate to="/orgs/$orgId/servers/$serverId/addons/frameworks" params={params} replace />
      );
    }
    return first.key === "workshop" ? (
      <Navigate to="/orgs/$orgId/servers/$serverId/addons/workshop" params={params} replace />
    ) : (
      <Navigate
        to="/orgs/$orgId/servers/$serverId/addons/$kind"
        params={{ ...params, kind: first.key }}
        replace
      />
    );
  },
});
