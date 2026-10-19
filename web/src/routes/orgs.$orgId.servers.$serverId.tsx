import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link, Outlet, redirect } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import { NotFound } from "@/components/error-screen";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { addonKinds } from "@/lib/eggs";
import { canonical, resolveServer, shortId, useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import { type ServerConfig, serverStatus } from "@/lib/servers";

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
  // The egg's add-ons (plugins, mods) get a tab each after Files, and
  // Steam Workshop items one for those who change its startup.
  const details = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: can(server, "files.read") || can(server, "startup") },
  );
  const { addonTabs, workshop } = useMemo(() => {
    const cfg = details.data?.configJson
      ? (JSON.parse(details.data.configJson) as ServerConfig)
      : undefined;
    return {
      addonTabs: can(server, "files.read")
        ? Object.keys(cfg?.egg?.addons ?? {}).filter((k) => addonKinds[k])
        : [],
      workshop: !!cfg?.egg?.workshop && can(server, "startup"),
    };
  }, [details.data?.configJson, server]);
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
              ...(t.label === "Files"
                ? addonTabs.map((kind) => (
                    <Link
                      key={kind}
                      to="/orgs/$orgId/servers/$serverId/addons/$kind"
                      params={{ ...params, kind }}
                      className={tabClass}
                    >
                      {addonKinds[kind]?.tab}
                    </Link>
                  ))
                : []),
              ...(t.label === "Files" && workshop
                ? [
                    <Link
                      key="workshop"
                      to="/orgs/$orgId/servers/$serverId/workshop"
                      params={params}
                      className={tabClass}
                    >
                      Workshop
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
