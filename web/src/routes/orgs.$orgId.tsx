import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { ShieldAlert } from "lucide-react";

import { AppShell } from "@/components/app-shell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { canonical, resolveOrg, shortId, useIds } from "@/lib/ids";
import { requireSession } from "@/lib/session";

// Every org page sits in the app shell, with the org's sidebar. Its URL
// has the org's short ID (lib/ids.ts), and the full one is looked up first.
export const Route = createFileRoute("/orgs/$orgId")({
  params: {
    parse: (p) => p,
    stringify: ({ orgId }) => ({ orgId: shortId(orgId) }),
  },
  beforeLoad: async ({ location, params }) => {
    const session = await requireSession(location);
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
    await resolveOrg(params.orgId);
    return session;
  },
  component: OrgLayout,
});

function OrgLayout() {
  const session = Route.useRouteContext();
  const { orgId } = useIds();
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
