import { createFileRoute } from "@tanstack/react-router";

import { ServerSettings } from "@/components/server-settings";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/startup")({
  component: StartupTab,
});

function StartupTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = Route.useParams();
  const { server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  return (
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
  );
}
