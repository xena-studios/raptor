import { createFileRoute } from "@tanstack/react-router";

import { ServerBackups } from "@/components/server-backups";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/backups")({
  component: BackupsTab,
});

function BackupsTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = Route.useParams();
  return (
    <ServerBackups
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      userId={session.user?.id ?? ""}
    />
  );
}
