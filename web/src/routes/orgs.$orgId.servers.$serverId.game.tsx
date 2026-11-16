import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { GameSettings } from "@/components/game-settings";
import { ServerListLook } from "@/components/server-list-look";
import { VersionSwitch } from "@/components/version-switch";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isProxy } from "@/lib/game-tab";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import type { ServerConfig } from "@/lib/servers";

// A server's Game tab (#436): the game's own settings, as a form, and how
// it looks in players' server list.
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/game")({
  component: function Game() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
    const proxy = isProxy(
      res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined,
    );
    if (!server) return null;
    return (
      <div className="flex flex-col gap-6">
        <VersionSwitch orgId={orgId} nodeId={nodeId} serverId={serverId} server={server} />
        <ServerListLook
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          canWrite={can(server, "files.write")}
        />
        {!proxy && (
          <GameSettings
            orgId={orgId}
            nodeId={nodeId}
            serverId={serverId}
            canWrite={can(server, "files.write")}
          />
        )}
      </div>
    );
  },
});
