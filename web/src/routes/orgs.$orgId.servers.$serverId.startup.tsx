import { createFileRoute, Link } from "@tanstack/react-router";

import { TabIntro } from "@/components/page";

import { ServerSettings } from "@/components/server-settings";
import { useGameTab } from "@/lib/game-tab";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/startup")({
  component: StartupTab,
});

function StartupTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { server } = useServer(orgId, nodeId, serverId);
  const game = useGameTab(orgId, nodeId, serverId, server);
  if (!server) return null;
  return (
    <div className="flex flex-col gap-6">
      <TabIntro>
        Every startup variable and the startup command, exactly as the server runs them.{" "}
        {game && (
          <>
            The{" "}
            <Link
              to="/orgs/$orgId/servers/$serverId/game"
              params={{ orgId, serverId }}
              className="font-medium text-foreground underline-offset-4 hover:underline"
            >
              Game
            </Link>{" "}
            tab shows the same values as a friendlier form.
          </>
        )}
      </TabIntro>
      <ServerSettings
        section="startup"
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        userId={session.user?.id ?? ""}
        admin={can(server, "*")}
        canReinstall={false}
        stopped={false}
      />
    </div>
  );
}
