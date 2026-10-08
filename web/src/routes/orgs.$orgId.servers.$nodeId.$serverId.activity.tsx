import { createFileRoute } from "@tanstack/react-router";

import { Log } from "@/components/org/audit-log";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  return <Log orgId={orgId} nodeId={nodeId} serverId={serverId} />;
}
