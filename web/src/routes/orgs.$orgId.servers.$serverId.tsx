import { createFileRoute, Link, Outlet, redirect } from "@tanstack/react-router";
import { useState } from "react";

import { NotFound } from "@/components/error-screen";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import type { Server } from "@/gen/raptor/panel/v1/org_pb";
import { useAddonSections } from "@/lib/addon-sections";
import { useGameTab } from "@/lib/game-tab";
import { canonical, resolveServer, shortId, useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import { usePlayersTab } from "@/lib/players-tab";
import { serverStatus } from "@/lib/servers";

// A server's tabs, each shown to those who may use it.
const base = "/orgs/$orgId/servers/$serverId";
const tabs = [
  { label: "Overview", to: base, exact: true, show: () => true },
  { label: "Files", to: `${base}/files`, show: (s) => can(s, "files.read") },
  { label: "Schedules", to: `${base}/schedules`, show: (s) => can(s, "schedules") },
  { label: "Backups", to: `${base}/backups`, show: (s) => can(s, "backups") },
  {
    label: "Databases",
    to: `${base}/databases`,
    show: (s) => can(s, "databases.read") || can(s, "databases.write"),
  },
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
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId")({
  params: {
    parse: (p) => p,
    stringify: ({ serverId }) => ({ serverId: shortId(serverId) }),
  },
  beforeLoad: async ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
    await resolveServer(params.orgId, params.serverId);
  },
  component: ServerLayout,
});

function ServerLayout() {
  const { orgId, nodeId, serverId } = useIds();
  const { node, server, loaded } = useServer(orgId, nodeId, serverId);
  // The egg's add-ons (plugins, mods, datapacks) and Steam Workshop items
  // share one Add-ons tab after Files, with a section each (#436).
  const { sections } = useAddonSections(orgId, nodeId, serverId, server, node?.capabilities);
  const game = useGameTab(orgId, nodeId, serverId, server);
  const players = usePlayersTab(orgId, nodeId, serverId, server);
  const [error, setError] = useState("");
  const status = server && serverStatus(server, node?.connected ?? false);
  const params = { orgId, serverId };

  if (loaded && !server) return <NotFound full={false} what="server" />;
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
              <PowerButtons
                nodeId={nodeId}
                serverId={serverId}
                state={
                  ["pending", "installing", "failed"].includes(server?.installState ?? "")
                    ? "installing"
                    : server?.state
                }
                offline={!node?.connected}
                onError={setError}
              />
            )
          }
        />
        {status && <StatusDetail status={status} />}
        {error && <p className="text-sm text-destructive">{error}</p>}
        <RouteTabs label="Server sections">
          {tabs
            .filter((t) => t.show(server))
            .map((t) => [
              <Link
                key={t.to}
                to={t.to}
                params={params}
                activeOptions={{ exact: "exact" in t && t.exact }}
                className={tabClass}
              >
                {t.label}
              </Link>,
              ...(t.label === "Overview" && game
                ? [
                    <Link
                      key="game"
                      to="/orgs/$orgId/servers/$serverId/game"
                      params={params}
                      className={tabClass}
                    >
                      Game
                    </Link>,
                  ]
                : []),
              ...(t.label === "Overview" && players
                ? [
                    <Link
                      key="players"
                      to="/orgs/$orgId/servers/$serverId/players"
                      params={params}
                      className={tabClass}
                    >
                      Players
                    </Link>,
                  ]
                : []),
              ...(t.label === "Files" && sections.length > 0
                ? [
                    <Link
                      key="addons"
                      to="/orgs/$orgId/servers/$serverId/addons"
                      params={params}
                      className={tabClass}
                    >
                      Add-ons
                    </Link>,
                  ]
                : []),
            ])}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}
