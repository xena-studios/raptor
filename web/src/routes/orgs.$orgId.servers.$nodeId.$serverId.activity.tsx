import { createFileRoute } from "@tanstack/react-router";

import { OrgActivity } from "@/components/org/audit-log";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  return <OrgActivity orgId={orgId} nodeId={nodeId} serverId={serverId} />;
}
