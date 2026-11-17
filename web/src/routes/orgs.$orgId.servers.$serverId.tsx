import { createFileRoute, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import { useState } from "react";

import { NotFound } from "@/components/error-screen";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAddonSections } from "@/lib/addon-sections";
import { useGameTab } from "@/lib/game-tab";
import { canonical, resolveServer, shortId, useIds } from "@/lib/ids";
import { useMinecraftServer } from "@/lib/minecraft-tab";
import { can, useServer } from "@/lib/org-data";
import { usePlayersTab } from "@/lib/players-tab";
import { activeServerTab, type ServerTab, type ServerTabKey, serverTabs } from "@/lib/server-tabs";
import { serverStatus } from "@/lib/servers";

// Each tab's route, for typed links.
const base = "/orgs/$orgId/servers/$serverId";
const routes = {
  overview: base,
  game: `${base}/game`,
  addons: `${base}/addons`,
  players: `${base}/players`,
  worlds: `${base}/worlds`,
  files: `${base}/files`,
  backups: `${base}/backups`,
  schedules: `${base}/schedules`,
  databases: `${base}/databases`,
  network: `${base}/network`,
  startup: `${base}/startup`,
  settings: `${base}/settings`,
  activity: `${base}/activity`,
  access: `${base}/access`,
} as const satisfies Record<ServerTabKey, string>;

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
  // share one Add-ons tab, with a section each (#436).
  const { sections } = useAddonSections(orgId, nodeId, serverId, server, node?.capabilities);
  const game = useGameTab(orgId, nodeId, serverId, server);
  const players = usePlayersTab(orgId, nodeId, serverId, server);
  const minecraft = useMinecraftServer(orgId, nodeId, serverId, server);
  const [error, setError] = useState("");
  const pathname = useRouterState({ select: (r) => r.location.pathname });
  const open = activeServerTab(pathname);
  const tabs = serverTabs({
    can: (p) => can(server, p),
    game,
    addons: sections.length > 0,
    players,
    worlds: minecraft,
  });
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
          {tabs.bar.map((t) => (
            <Link
              key={t.key}
              to={routes[t.key]}
              params={params}
              activeOptions={{ exact: t.key === "overview" }}
              className={tabClass}
            >
              {t.label}
            </Link>
          ))}
          {tabs.more.length > 0 && <MoreTabs tabs={tabs.more} active={open} params={params} />}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}

// MoreTabs is the tab bar's last item: a menu of the tabs used less often
// (#436). While one of them is open, the trigger is the active tab and
// names it, so the page you are on is never hidden.
function MoreTabs({
  tabs,
  active,
  params,
}: {
  tabs: ServerTab[];
  active: ServerTabKey | undefined;
  params: { orgId: string; serverId: string };
}) {
  const current = tabs.find((t) => t.key === active);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={`${tabClass} flex shrink-0 items-center gap-1 outline-none focus-visible:text-foreground focus-visible:underline`}
        data-status={current ? "active" : undefined}
        aria-label={current ? `More sections (open: ${current.label})` : "More sections"}
      >
        {current ? `More: ${current.label}` : "More"}
        <ChevronDown className="size-3.5" aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        {tabs.map((t) => (
          <DropdownMenuItem
            key={t.key}
            render={<Link to={routes[t.key]} params={params} />}
            aria-current={t.key === active ? "page" : undefined}
            className={t.key === active ? "font-medium" : undefined}
          >
            {t.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
