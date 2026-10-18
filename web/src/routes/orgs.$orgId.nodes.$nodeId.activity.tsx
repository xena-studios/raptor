import { createFileRoute } from "@tanstack/react-router";

import { OrgActivity } from "@/components/org/audit-log";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId, nodeId } = useIds();
  return <OrgActivity orgId={orgId} nodeId={nodeId} />;
}
