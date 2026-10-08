import { createFileRoute, Link, Outlet } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { requireSession } from "@/lib/session";

// The account's settings: general, security, and activity.
export const Route = createFileRoute("/settings")({
  beforeLoad: ({ location }) => requireSession(location),
  component: SettingsLayout,
});

function SettingsLayout() {
  const session = Route.useRouteContext();
  return (
    <AppShell session={session}>
      <div className="space-y-4">
        <PageHeader
          eyebrow="Account"
          title="Account settings"
          description="Your profile, how you sign in, and what's happened on your account."
        />
        <RouteTabs label="Account sections">
          <Link to="/settings" activeOptions={{ exact: true }} className={tabClass}>
            General
          </Link>
          <Link to="/settings/security" className={tabClass}>
            Security
          </Link>
          <Link to="/settings/activity" className={tabClass}>
            Activity
          </Link>
        </RouteTabs>
      </div>
      <Outlet />
    </AppShell>
  );
}
