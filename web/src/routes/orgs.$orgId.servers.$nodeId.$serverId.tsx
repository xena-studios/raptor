import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useState } from "react";

import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import type { Server } from "@/gen/raptor/panel/v1/org_pb";
import { can, useServer } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

// A server's tabs, each shown to those who may use it.
const base = "/orgs/$orgId/servers/$nodeId/$serverId";
const tabs = [
  { label: "Overview", to: base, exact: true, show: () => true },
  { label: "Files", to: `${base}/files`, show: (s) => can(s, "files.read") },
  { label: "Databases", to: `${base}/databases`, show: (s) => can(s, "*") },
  { label: "Schedules", to: `${base}/schedules`, show: (s) => can(s, "schedules") },
  { label: "Backups", to: `${base}/backups`, show: (s) => can(s, "backups") },
  { label: "Network", to: `${base}/network`, show: () => true },
  { label: "Startup", to: `${base}/startup`, show: (s) => can(s, "startup") },
  {
    label: "Settings",
    to: `${base}/settings`,
    show: (s) => can(s, "startup") || can(s, "reinstall"),
  },
  { label: "Activity", to: `${base}/activity`, show: (s) => can(s, "*") },
  { label: "Access", to: `${base}/access`, show: (s) => can(s, "*") },
] as const satisfies readonly {
  label: string;
  to: string;
  exact?: boolean;
  show: (s: Server | undefined) => boolean;
}[];

// A server's pages: its header with the power buttons, its tabs, and the
// tab below.
export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId")({
  component: ServerLayout,
});

function ServerLayout() {
  const { orgId, nodeId, serverId } = Route.useParams();
  const { node, server, loaded } = useServer(orgId, nodeId, serverId);
  const [error, setError] = useState("");
  const status = server && serverStatus(server, node?.connected ?? false);
  const params = { orgId, nodeId, serverId };

  if (loaded && !server) {
    return (
      <PageHeader
        back={{ label: "Servers", to: "/orgs/$orgId/servers", params: { orgId } }}
        title="Server not found"
        description="It doesn't exist, or you don't have access to it."
      />
    );
  }
  return (
    <>
      <div className="space-y-4">
        <PageHeader
          back={{ label: "Servers", to: "/orgs/$orgId/servers", params: { orgId } }}
          title={
            <span className="flex items-center gap-2.5">
              {server?.name ?? "Server"}
              {status && <StatusBadge status={status} />}
            </span>
          }
          description={[server?.eggName, node?.name].filter(Boolean).join(" · ")}
          actions={
            can(server, "power") && (
              <PowerButtons nodeId={nodeId} serverId={serverId} onError={setError} />
            )
          }
        />
        {status && <StatusDetail status={status} />}
        {error && <p className="text-sm text-destructive">{error}</p>}
        <RouteTabs label="Server sections">
          {tabs
            .filter((t) => t.show(server))
            .map((t) => (
              <Link
                key={t.to}
                to={t.to}
                params={params}
                activeOptions={{ exact: "exact" in t && t.exact }}
                className={tabClass}
              >
                {t.label}
              </Link>
            ))}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}
