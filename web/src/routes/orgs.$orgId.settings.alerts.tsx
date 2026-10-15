import { createFileRoute } from "@tanstack/react-router";

import { Alerts } from "@/components/org/alerts";

export const Route = createFileRoute("/orgs/$orgId/settings/alerts")({
  component: AlertsTab,
});

function AlertsTab() {
  const { orgId } = Route.useParams();
  return <Alerts orgId={orgId} />;
}
