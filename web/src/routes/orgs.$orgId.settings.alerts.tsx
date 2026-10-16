import { createFileRoute } from "@tanstack/react-router";

import { Alerts } from "@/components/org/alerts";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/settings/alerts")({
  component: AlertsTab,
});

function AlertsTab() {
  const { orgId } = useIds();
  return <Alerts orgId={orgId} />;
}
