import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { AppShell } from "@/components/app-shell";
import { NotFound } from "@/components/error-screen";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { requireSession } from "@/lib/session";
import { staffClient } from "@/lib/transport";

// Raptor's admin panel (docs/PANEL.md#staff), for staff only: the platform
// at a glance, accounts, orgs, and the staff log. Staff confirm with a
// passkey to get in (every 12 hours), and again before any change.
export const Route = createFileRoute("/admin")({
  beforeLoad: ({ location }) => requireSession(location),
  component: AdminLayout,
});

function AdminLayout() {
  const session = Route.useRouteContext();
  const me = useQuery(StaffService.method.getStaffMe, {});
  return (
    <AppShell session={session}>
      {me.isPending ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : !me.data?.role ? (
        <NotFound full={false} what="page" />
      ) : (
        <>
          <div className="space-y-4">
            <PageHeader
              eyebrow="Raptor"
              title={
                <span className="flex items-center gap-2.5">
                  Admin
                  <Badge variant="outline" className="capitalize">
                    {me.data.role}
                  </Badge>
                </span>
              }
              description="The platform, its accounts, and its orgs. Everything you do here is logged."
            />
            <RouteTabs label="Admin sections">
              <Link to="/admin" activeOptions={{ exact: true }} className={tabClass}>
                Overview
              </Link>
              <Link to="/admin/users" className={tabClass}>
                Users
              </Link>
              <Link to="/admin/orgs" className={tabClass}>
                Orgs
              </Link>
              <Link to="/admin/support" className={tabClass}>
                Support
              </Link>
              <Link to="/admin/announcements" className={tabClass}>
                Announcements
              </Link>
              <Link to="/admin/staff" className={tabClass}>
                Staff
              </Link>
            </RouteTabs>
          </div>
          {me.data.passkeyFresh ? <Outlet /> : <PasskeyGate />}
        </>
      )}
    </AppShell>
  );
}

// PasskeyGate asks staff to confirm with their passkey, at most every 12
// hours.
function PasskeyGate() {
  const withReauth = useReauth();
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <Card className="max-w-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck className="size-4" /> Confirm it's you
        </CardTitle>
        <CardDescription>
          The admin panel needs a passkey confirmed in the last 12 hours. Changes ask again.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <Button
          className="self-start"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError("");
            try {
              await withReauth(() => staffClient.getOverview({ days: 1 }));
              await client.invalidateQueries();
            } catch (err) {
              setError(message(err));
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy ? <Loader2 className="animate-spin" /> : <KeyRound />} Use my passkey
        </Button>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
