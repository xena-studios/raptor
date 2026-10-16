import { createFileRoute } from "@tanstack/react-router";

import { ServerSchedules } from "@/components/server-schedules";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/schedules")({
  component: SchedulesTab,
});

function SchedulesTab() {
  const { orgId, nodeId, serverId } = useIds();
  return <ServerSchedules orgId={orgId} nodeId={nodeId} serverId={serverId} />;
}
