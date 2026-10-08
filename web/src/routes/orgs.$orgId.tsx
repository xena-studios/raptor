import { createFileRoute, Outlet } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { requireSession } from "@/lib/session";

// Every org page sits in the app shell, with the org's sidebar.
export const Route = createFileRoute("/orgs/$orgId")({
  beforeLoad: ({ location }) => requireSession(location),
  component: OrgLayout,
});

function OrgLayout() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  return (
    <AppShell session={session} orgId={orgId}>
      <Outlet />
    </AppShell>
  );
}
