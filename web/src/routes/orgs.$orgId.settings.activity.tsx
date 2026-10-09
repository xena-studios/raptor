import { createFileRoute } from "@tanstack/react-router";

import { OrgActivity } from "@/components/org/audit-log";

export const Route = createFileRoute("/orgs/$orgId/settings/activity")({
  component: ActivityTab,
});

function ActivityTab() {
  const { orgId } = Route.useParams();
  return <OrgActivity orgId={orgId} />;
}
