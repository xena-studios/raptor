import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Navigate } from "@tanstack/react-router";

import { OrgSettings } from "@/components/org/org-settings";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin } from "@/lib/format";

export const Route = createFileRoute("/orgs/$orgId/settings/")({
  component: General,
});

function General() {
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  if (!org) return null;
  // Members can't change the org: their settings page is the member list.
  if (!isAdmin(org.role))
    return <Navigate to="/orgs/$orgId/settings/members" params={{ orgId }} replace />;
  return <OrgSettings key={org.name} orgId={orgId} name={org.name} />;
}
