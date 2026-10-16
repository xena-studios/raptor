import { createFileRoute } from "@tanstack/react-router";

import { ServerBackups } from "@/components/server-backups";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/backups")({
  component: BackupsTab,
});

function BackupsTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  return (
    <ServerBackups
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      userId={session.user?.id ?? ""}
    />
  );
}
