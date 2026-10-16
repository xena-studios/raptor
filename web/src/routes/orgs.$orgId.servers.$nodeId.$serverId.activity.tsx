import { createFileRoute } from "@tanstack/react-router";

import { OrgActivity } from "@/components/org/audit-log";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId, nodeId, serverId } = useIds();
  return <OrgActivity orgId={orgId} nodeId={nodeId} serverId={serverId} />;
}
