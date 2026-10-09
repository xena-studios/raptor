import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
  Check,
  ChevronsUpDown,
  Egg,
  LayoutDashboard,
  LogOut,
  Plus,
  Server,
  Settings,
  SquareTerminal,
  UserRound,
  WifiOff,
} from "lucide-react";
import { type ReactNode, useEffect } from "react";

import { NotFound } from "@/components/error-screen";
import { Logo } from "@/components/logo";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { AuthService, type GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { useApiDown } from "@/lib/connection";
import { roleNames } from "@/lib/format";
import { getTheme, isTheme, setTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

// The org the app shows when a page isn't about one (the last one opened).
const lastOrgKey = "raptor.org";

export function lastOrg(): string | null {
  try {
    return localStorage.getItem(lastOrgKey);
  } catch {
    return null;
  }
}

let themeSynced = false;

// The frame around signed-in pages: a sidebar (the org switcher, the org's
// sections and nodes, how many nodes are online, the user's menu), a top bar,
// and the page.
export function AppShell({
  session,
  orgId,
  children,
}: {
  session: GetSessionResponse;
  // The org the page is about; otherwise the last one opened.
  orgId?: string;
  children: ReactNode;
}) {
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const list = orgs.data?.orgs ?? [];
  const currentId = orgId ?? lastOrg() ?? list[0]?.id;
  const current = list.find((o) => o.id === currentId) ?? list[0];

  // The account's theme wins once per page load; after that, the
  // Appearance card changes both.
  useEffect(() => {
    const t = session.user?.theme;
    if (themeSynced || !isTheme(t)) return;
    themeSynced = true;
    if (t !== getTheme()) setTheme(t);
  }, [session.user?.theme]);

  useEffect(() => {
    if (!orgId) return;
    try {
      localStorage.setItem(lastOrgKey, orgId);
    } catch {
      // Private browsing: nothing to remember it in.
    }
  }, [orgId]);

  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton size="lg" render={<Link to="/" />}>
                <Logo className="size-8" />
                <span className="font-heading text-base font-semibold">Raptor</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <OrgSwitcher orgs={list} current={current?.id} />
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarHeader>
        <SidebarContent>{current && <OrgNav orgId={current.id} />}</SidebarContent>
        <SidebarFooter>
          {current && <NodeCount orgId={current.id} />}
          <UserMenu session={session} />
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>
      <SidebarInset>
        <header className="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b bg-background px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-1 h-4" />
          <span className="truncate text-sm text-muted-foreground">{current?.name}</span>
        </header>
        <ConnectionBanner />
        <main className="mx-auto w-full max-w-6xl flex-1 space-y-6 p-6">
          {orgId && orgs.data && !list.some((o) => o.id === orgId) ? (
            <NotFound full={false} what="org" />
          ) : (
            children
          )}
        </main>
      </SidebarInset>
    </SidebarProvider>
  );
}

type OrgSummary = { id: string; name: string; role: number };

function OrgSwitcher({ orgs, current }: { orgs: OrgSummary[]; current?: string }) {
  const navigate = useNavigate();
  const org = orgs.find((o) => o.id === current);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <SidebarMenuButton size="lg" className="border">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-xs font-semibold">
              {initials(org?.name ?? "?")}
            </span>
            <span className="grid min-w-0 flex-1 text-left leading-tight">
              <span className="truncate font-medium">{org?.name ?? "No org"}</span>
              {org && (
                <span className="truncate text-xs text-muted-foreground">
                  {roleNames[org.role as keyof typeof roleNames]}
                </span>
              )}
            </span>
            <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
          </SidebarMenuButton>
        }
      />
      <DropdownMenuContent align="start" className="w-60">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Orgs</DropdownMenuLabel>
          {orgs.map((o) => (
            <DropdownMenuItem
              key={o.id}
              onClick={() => navigate({ to: "/orgs/$orgId", params: { orgId: o.id } })}
            >
              <span className="min-w-0 flex-1 truncate">{o.name}</span>
              {o.id === current && <Check className="size-4" />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate({ to: "/", search: { new: true } })}>
          <Plus className="size-4" /> New org
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ConnectionBanner says when the Panel stopped answering, until it answers
// again; pages keep what they last loaded meanwhile.
function ConnectionBanner() {
  const down = useApiDown();
  if (!down) return null;
  return (
    <div
      role="status"
      className="flex items-center justify-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm"
    >
      <WifiOff className="size-4 shrink-0 text-amber-500" />
      Can't reach Raptor right now. What you see may be out of date; this clears when it's back.
    </div>
  );
}

// OrgNav is the org's sections; the org's nodes are listed under Nodes.
function OrgNav({ orgId }: { orgId: string }) {
  const path = useLocation({ select: (l) => l.pathname });
  const nodes = useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 15_000 });
  const base = `/orgs/${orgId}`;
  const active = (p: string) =>
    p === base ? path === base || path === `${base}/` : path.startsWith(p);

  return (
    <SidebarGroup>
      <SidebarGroupLabel>Manage</SidebarGroupLabel>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton
            tooltip="Overview"
            isActive={active(base)}
            render={<Link to="/orgs/$orgId" params={{ orgId }} />}
          >
            <LayoutDashboard /> <span>Overview</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            tooltip="Nodes"
            isActive={active(`${base}/nodes`)}
            render={<Link to="/orgs/$orgId/nodes" params={{ orgId }} />}
          >
            <SquareTerminal /> <span>Nodes</span>
          </SidebarMenuButton>
          {(nodes.data?.nodes.length ?? 0) > 0 && (
            <SidebarMenuSub>
              {nodes.data?.nodes.map((n) => (
                <SidebarMenuSubItem key={n.id}>
                  <SidebarMenuSubButton
                    isActive={path.startsWith(`${base}/nodes/${n.id}`)}
                    render={
                      <Link to="/orgs/$orgId/nodes/$nodeId" params={{ orgId, nodeId: n.id }} />
                    }
                  >
                    <span
                      className={cn(
                        "size-2 shrink-0 rounded-full",
                        n.connected ? "bg-emerald-500" : "bg-muted-foreground/40",
                      )}
                      title={n.connected ? "Connected" : "Offline"}
                    />
                    <span className="truncate">{n.name}</span>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              ))}
            </SidebarMenuSub>
          )}
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            tooltip="Servers"
            isActive={active(`${base}/servers`)}
            render={<Link to="/orgs/$orgId/servers" params={{ orgId }} />}
          >
            <Server /> <span>Servers</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            tooltip="Eggs"
            isActive={active(`${base}/eggs`)}
            render={<Link to="/orgs/$orgId/eggs" params={{ orgId }} />}
          >
            <Egg /> <span>Eggs</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            tooltip="Settings"
            isActive={active(`${base}/settings`)}
            render={<Link to="/orgs/$orgId/settings" params={{ orgId }} />}
          >
            <Settings /> <span>Settings</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarGroup>
  );
}

// NodeCount is how many of the org's nodes are connected.
function NodeCount({ orgId }: { orgId: string }) {
  const nodes = useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 15_000 });
  const list = nodes.data?.nodes ?? [];
  if (list.length === 0) return null;
  const online = list.filter((n) => n.connected).length;
  return (
    <p className="px-2 text-xs text-muted-foreground tabular-nums group-data-[collapsible=icon]:hidden">
      <span className={cn(online > 0 && "text-emerald-500")}>{online} online</span> of {list.length}{" "}
      {list.length === 1 ? "node" : "nodes"}
    </p>
  );
}

function UserMenu({ session }: { session: GetSessionResponse }) {
  const navigate = useNavigate();
  const signOut = useMutation(AuthService.method.signOut);
  const email = session.user?.email ?? "";
  const name = session.user?.name ?? "";
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton size="lg">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold uppercase">
                  {initials(name || email)}
                </span>
                <span className="grid min-w-0 flex-1 text-left leading-tight">
                  {name && <span className="truncate font-medium">{name}</span>}
                  <span className={cn("truncate", name && "text-xs text-muted-foreground")}>
                    {email}
                  </span>
                </span>
                <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
              </SidebarMenuButton>
            }
          />
          <DropdownMenuContent align="start" side="top" className="w-60">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="grid leading-tight">
                {name && <span className="truncate font-medium text-foreground">{name}</span>}
                <span className="truncate">{email}</span>
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => navigate({ to: "/settings" })}>
              <UserRound className="size-4" /> Account settings
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={async () => {
                await signOut.mutateAsync({});
                await navigate({ to: "/signin" });
              }}
            >
              <LogOut className="size-4" /> Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  return (
    (words.length > 1 ? (words[0]?.[0] ?? "") + (words[1]?.[0] ?? "") : name.slice(0, 2)) || "?"
  ).toUpperCase();
}
