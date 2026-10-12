import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";

import { AppShell } from "@/components/app-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { requireSession } from "@/lib/session";

// Every org page sits in the app shell, with the org's sidebar.
export const Route = createFileRoute("/orgs/$orgId")({
  beforeLoad: ({ location }) => requireSession(location),
  component: OrgLayout,
});

function OrgLayout() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const suspended = orgs.data?.orgs.find((o) => o.id === orgId)?.suspended;
  return (
    <AppShell session={session} orgId={orgId}>
      {suspended ? (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertTitle>This org is suspended</AlertTitle>
          <AlertDescription>
            Raptor's staff suspended it, so it can't be managed here for now. Its servers keep
            running. Write to support@raptorpanel.net about it.
          </AlertDescription>
        </Alert>
      ) : (
        <Outlet />
      )}
    </AppShell>
  );
}
