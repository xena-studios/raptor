import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { Members } from "@/components/org/members";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";

export const Route = createFileRoute("/orgs/$orgId/settings/members")({
  component: MembersTab,
});

function MembersTab() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  return <Members orgId={orgId} myRole={org?.role} myId={session.user?.id ?? ""} />;
}
