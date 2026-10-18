import { createFileRoute } from "@tanstack/react-router";

import { DeleteServer } from "@/components/delete-server";
import { MoveServer } from "@/components/move-server";
import { ServerSettings } from "@/components/server-settings";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/settings")({
  component: SettingsTab,
});

function SettingsTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  const userId = session.user?.id ?? "";
  return (
    <div className="flex flex-col gap-6">
      <ServerSettings
        section="general"
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        userId={userId}
        admin={can(server, "*")}
        canReinstall={can(server, "reinstall")}
        stopped={["offline", "crashed", "install_failed", ""].includes(server.state)}
      />
      {can(server, "*") && <MoveServer orgId={orgId} nodeId={nodeId} serverId={serverId} />}
      {can(server, "*") && (
        <DeleteServer
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          name={server.name}
          userId={userId}
        />
      )}
    </div>
  );
}
