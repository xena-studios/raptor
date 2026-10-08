import { createFileRoute } from "@tanstack/react-router";

import { ServerSchedules } from "@/components/server-schedules";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/schedules")({
  component: SchedulesTab,
});

function SchedulesTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  return <ServerSchedules orgId={orgId} nodeId={nodeId} serverId={serverId} />;
}
