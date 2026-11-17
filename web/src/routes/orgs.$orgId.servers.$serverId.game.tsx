import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { GameSettings } from "@/components/game-settings";
import { TabIntro } from "@/components/page";
import { ProxyBackends } from "@/components/proxy-backends";
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
    const cfg = res.data?.configJson
      ? (JSON.parse(res.data.configJson) as ServerConfig)
      : undefined;
    const proxy = isProxy(cfg);
    const first = Object.values(cfg?.egg?.addons ?? {}).map((a) => a.loaders[0]);
    const backends = first.includes("velocity")
      ? "velocity"
      : first.includes("waterfall") || first.includes("bungeecord")
        ? "bungee"
        : undefined;
    if (!server) return null;
    return (
      <div className="flex flex-col gap-6">
        <TabIntro>
          The game's own settings, as a form.{" "}
          {can(server, "startup") && (
            <>
              Every startup variable, exactly as set, is under More, in{" "}
              <Link
                to="/orgs/$orgId/servers/$serverId/startup"
                params={{ orgId, serverId }}
                className="font-medium text-foreground underline-offset-4 hover:underline"
              >
                Startup
              </Link>
              .
            </>
          )}
        </TabIntro>
        <VersionSwitch orgId={orgId} nodeId={nodeId} serverId={serverId} server={server} />
        {backends && (
          <ProxyBackends
            kind={backends}
            orgId={orgId}
            nodeId={nodeId}
            serverId={serverId}
            canWrite={can(server, "files.write")}
          />
        )}
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
