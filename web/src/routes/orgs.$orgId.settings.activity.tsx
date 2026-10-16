import { createFileRoute } from "@tanstack/react-router";

import { OrgActivity } from "@/components/org/audit-log";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/settings/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId } = useIds();
  return <OrgActivity orgId={orgId} />;
}
