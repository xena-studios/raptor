import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { LeaveOrg, OrgDetails, OrgSettings } from "@/components/org/org-settings";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin } from "@/lib/format";

export const Route = createFileRoute("/orgs/$orgId/settings/")({
  component: General,
});

function General() {
  const { orgId } = Route.useParams();
  const { user } = Route.useRouteContext();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  if (!org || !user) return null;
  return (
    <div className="max-w-2xl space-y-6">
      <OrgSettings key={org.name} org={org} editable={isAdmin(org.role)} />
      <OrgDetails org={org} />
      <LeaveOrg org={org} userId={user.id} />
    </div>
  );
}
