import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";

import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin } from "@/lib/format";

// The org's settings: general, members, billing, and the audit log.
export const Route = createFileRoute("/orgs/$orgId/settings")({
  component: SettingsLayout,
});

function SettingsLayout() {
  const { orgId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const admin = isAdmin(org?.role);
  return (
    <>
      <div className="space-y-4">
        <PageHeader eyebrow={org?.name} title="Settings" description="This org and its members." />
        <RouteTabs label="Settings sections">
          <Link
            to="/orgs/$orgId/settings"
            params={{ orgId }}
            activeOptions={{ exact: true }}
            className={tabClass}
          >
            General
          </Link>
          <Link to="/orgs/$orgId/settings/members" params={{ orgId }} className={tabClass}>
            Members
          </Link>
          <Link to="/orgs/$orgId/settings/billing" params={{ orgId }} className={tabClass}>
            Billing
          </Link>
          {admin && (
            <Link to="/orgs/$orgId/settings/backups" params={{ orgId }} className={tabClass}>
              Backups
            </Link>
          )}
          {admin && (
            <Link to="/orgs/$orgId/settings/activity" params={{ orgId }} className={tabClass}>
              Activity
            </Link>
          )}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}
