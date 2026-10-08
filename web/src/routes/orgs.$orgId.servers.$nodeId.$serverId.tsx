import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useState } from "react";

import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import { can, useServer } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

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
          <Link
            to="/orgs/$orgId/servers/$nodeId/$serverId"
            params={params}
            activeOptions={{ exact: true }}
            className={tabClass}
          >
            Console
          </Link>
          {can(server, "files.read") && (
            <Link
              to="/orgs/$orgId/servers/$nodeId/$serverId/files"
              params={params}
              className={tabClass}
            >
              Files
            </Link>
          )}
          {(can(server, "startup") || can(server, "reinstall")) && (
            <Link
              to="/orgs/$orgId/servers/$nodeId/$serverId/settings"
              params={params}
              className={tabClass}
            >
              Settings
            </Link>
          )}
          {can(server, "*") && (
            <Link
              to="/orgs/$orgId/servers/$nodeId/$serverId/access"
              params={params}
              className={tabClass}
            >
              Access
            </Link>
          )}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}
