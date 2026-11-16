import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";
import { Package } from "lucide-react";

import { EmptyState } from "@/components/page";
import { ReforgerMods } from "@/components/reforger-mods";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// An Arma Reforger server's Workshop mods (#417,
// docs/EGGS.md#arma-reforger-mods).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/addons/reforger")({
  component: function Reforger() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
    if (res.error) return <p className="text-sm text-destructive">{message(res.error)}</p>;
    if (!server || !res.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
    const cfg = res.data.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined;
    const config = cfg?.egg?.reforger?.config;
    if (!config) {
      return (
        <EmptyState
          icon={Package}
          title="This server's game doesn't take Arma Reforger mods"
          description="Only Arma Reforger servers have this page."
        />
      );
    }
    return (
      <ReforgerMods
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        config={config}
        canWrite={can(server, "files.write")}
      />
    );
  },
});
