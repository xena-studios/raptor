import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Link, type LinkProps, useLocation, useNavigate } from "@tanstack/react-router";
import {
  Building2,
  Check,
  ChevronRight,
  ChevronsUpDown,
  Egg,
  Gamepad2,
  Headset,
  LayoutDashboard,
  LogOut,
  type LucideIcon,
  Megaphone,
  Plus,
  Server,
  Settings,
  Shield,
  UserRound,
  Users,
  WifiOff,
} from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import { Announcements } from "@/components/announcements";
import { BillingBanner } from "@/components/billing-banner";
import { NotFound } from "@/components/error-screen";
import { Logo, RaptorMark, Wordmark } from "@/components/logo";
import { NodeClock } from "@/components/node-clock";
import { NewOrgDialog } from "@/components/org/new-org-dialog";
import { SourceLink } from "@/components/source-link";
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
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { AuthService, type GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { useApiDown } from "@/lib/connection";
import { roleNames } from "@/lib/format";
import { shortId } from "@/lib/ids";
import { useOrgNodes, useOrgServers } from "@/lib/org-data";
import { type ServerStatus, serverStatus } from "@/lib/servers";
import {
  getTheme,
  getThemeStyle,
  isTheme,
  isThemeStyle,
  setTheme,
  setThemeStyle,
} from "@/lib/theme";
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

// The frame around signed-in pages: a sidebar, a top bar, and the page.
// The sidebar's switcher picks where to work: one of the user's orgs, or
// for staff Raptor's admin panel (/admin). Below it are that place's
// sections (an org's also list its nodes); the user's menu is at the foot.
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
  const path = useLocation({ select: (l) => l.pathname });
  const admin = path === "/admin" || path.startsWith("/admin/");
  const title = admin
    ? "Raptor admin"
    : path === "/settings" || path.startsWith("/settings/")
      ? "Account settings"
      : orgId
        ? current?.name
        : undefined;

  // The account's theme wins once per page load; after that, the
  // Appearance card changes both.
  useEffect(() => {
    const t = session.user?.theme;
    const s = session.user?.themeStyle;
    if (themeSynced || !session.user) return;
    themeSynced = true;
    if (isTheme(t) && t !== getTheme()) setTheme(t);
    if (isThemeStyle(s) && s !== getThemeStyle()) setThemeStyle(s);
  }, [session.user]);

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
      <CloseOnNavigate path={path} />
      <Sidebar collapsible="icon">
        <SidebarHeader className="gap-3">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton size="lg" tooltip="Raptor" render={<Link to="/" />}>
                <Logo className="hidden size-8! shrink-0 group-data-[collapsible=icon]:block" />
                <RaptorMark className="h-7! w-auto! shrink-0 group-data-[collapsible=icon]:hidden" />
                <Wordmark className="h-3! w-auto! text-sidebar-foreground group-data-[collapsible=icon]:hidden" />
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
          <SidebarMenu>
            <SidebarMenuItem>
              {orgs.isPending ? (
                <div className="flex h-12 items-center gap-2 rounded-md border p-2 group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:border-0 group-data-[collapsible=icon]:p-0">
                  <Skeleton className="size-8 shrink-0 rounded-md" />
                  <Skeleton className="h-4 flex-1 group-data-[collapsible=icon]:hidden" />
                </div>
              ) : (
                <Switcher orgs={list} current={current?.id} admin={admin} />
              )}
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarHeader>
        <SidebarContent>
          {admin ? <AdminNav /> : current && <OrgNav orgId={current.id} />}
        </SidebarContent>
        <SidebarFooter>
          {!admin && current && <NodeCount orgId={current.id} />}
          <UserMenu session={session} />
          <SourceLink className="px-2 group-data-[collapsible=icon]:hidden" />
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>
      <SidebarInset>
        <header className="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b bg-background px-4">
          <SidebarTrigger className="-ml-1" />
          {title && (
            <>
              <Separator orientation="vertical" className="mr-1 h-4 self-center!" />
              <span className="truncate text-sm text-muted-foreground">{title}</span>
            </>
          )}
          {orgId && !admin && <NodeClock orgId={orgId} />}
        </header>
        <ConnectionBanner />
        <Announcements />
        {orgId && !admin && <BillingBanner orgId={orgId} />}
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

// CloseOnNavigate closes the sidebar on phones once a link in it is
// followed, so the page shows.
function CloseOnNavigate({ path }: { path: string }) {
  const { isMobile, setOpenMobile } = useSidebar();
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs on each new path
  useEffect(() => {
    if (isMobile) setOpenMobile(false);
  }, [path]);
  return null;
}

// useMenuSide is where the sidebar's menus open: beside a collapsed
// sidebar, otherwise next to their trigger.
function useMenuSide(open: "bottom" | "top"): "bottom" | "top" | "right" {
  const { state, isMobile } = useSidebar();
  return !isMobile && state === "collapsed" ? "right" : open;
}

type OrgSummary = { id: string; name: string; role: number };

// Tile is an org's initials, or the admin panel's shield, in a square.
function Tile({ name, admin, small }: { name?: string; admin?: boolean; small?: boolean }) {
  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md font-mono font-semibold",
        small ? "size-6 text-[10px]" : "size-8 text-xs",
        admin ? "bg-primary text-primary-foreground" : "bg-muted text-foreground",
      )}
    >
      {admin ? <Shield className={small ? "size-3.5!" : "size-4!"} /> : initials(name ?? "?")}
    </span>
  );
}

// Switcher is where to work: one of the user's orgs or, for staff, Raptor's
// admin panel, which it shows on admin pages.
function Switcher({
  orgs,
  current,
  admin,
}: {
  orgs: OrgSummary[];
  current?: string;
  admin: boolean;
}) {
  const navigate = useNavigate();
  const side = useMenuSide("bottom");
  const [making, setMaking] = useState(false);
  const staff = useQuery(StaffService.method.getStaffMe, {}, { staleTime: 5 * 60_000 });
  const role = staff.data?.role ?? "";
  const org = orgs.find((o) => o.id === current);
  const name = admin ? "Raptor admin" : (org?.name ?? "No org yet");
  const sub = admin
    ? role.charAt(0).toUpperCase() + role.slice(1)
    : org && roleNames[org.role as keyof typeof roleNames];
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <SidebarMenuButton
              size="lg"
              tooltip={name}
              className="border group-data-[collapsible=icon]:border-0"
            >
              <Tile name={org?.name} admin={admin} />
              <span className="grid min-w-0 flex-1 text-left leading-tight">
                <span className="truncate font-medium">{name}</span>
                {sub && <span className="truncate text-xs text-muted-foreground">{sub}</span>}
              </span>
              <ChevronsUpDown className="ml-auto text-muted-foreground" />
            </SidebarMenuButton>
          }
        />
        <DropdownMenuContent align="start" side={side} className="w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>Orgs</DropdownMenuLabel>
            {orgs.map((o) => (
              <DropdownMenuItem
                key={o.id}
                onClick={() => navigate({ to: "/orgs/$orgId", params: { orgId: o.id } })}
              >
                <Tile name={o.name} small />
                <span className="min-w-0 flex-1 truncate">{o.name}</span>
                {!admin && o.id === current && <Check />}
              </DropdownMenuItem>
            ))}
            <DropdownMenuItem onClick={() => setMaking(true)}>
              <span className="flex size-6 shrink-0 items-center justify-center rounded-md border border-dashed">
                <Plus className="size-3.5!" />
              </span>
              <span className="text-muted-foreground">New org</span>
            </DropdownMenuItem>
          </DropdownMenuGroup>
          {role && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuLabel>Staff</DropdownMenuLabel>
                <DropdownMenuItem onClick={() => navigate({ to: "/admin" })}>
                  <Tile admin small />
                  <span className="min-w-0 flex-1 truncate">Raptor admin</span>
                  {admin && <Check />}
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <NewOrgDialog open={making} onOpenChange={setMaking} />
    </>
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
      className="flex items-center justify-center gap-2 border-b border-warn/30 bg-warn-wash px-4 py-2 text-sm"
    >
      <WifiOff className="size-4 shrink-0 text-warn" />
      Can't reach Raptor right now. What you see may be out of date; this clears when it's back.
    </div>
  );
}

// NavLink is one of a section list's links, the same in every list.
function NavLink({
  link,
  label,
  icon: Icon,
  active,
  children,
}: {
  link: Pick<LinkProps, "to" | "params">;
  label: string;
  icon: LucideIcon;
  active: boolean;
  children?: ReactNode;
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton tooltip={label} isActive={active} render={<Link {...link} />}>
        <Icon /> <span>{label}</span>
      </SidebarMenuButton>
      {children}
    </SidebarMenuItem>
  );
}

// AdminNav is Raptor's admin panel's sections (docs/PANEL.md#staff).
const adminLinks = [
  { to: "/admin", label: "Overview", icon: LayoutDashboard },
  { to: "/admin/users", label: "Users", icon: Users },
  { to: "/admin/orgs", label: "Orgs", icon: Building2 },
  { to: "/admin/support", label: "Support", icon: Headset },
  { to: "/admin/eggs", label: "Eggs", icon: Egg },
  { to: "/admin/announcements", label: "Announcements", icon: Megaphone },
  { to: "/admin/settings", label: "Settings", icon: Settings },
] as const;

function AdminNav() {
  const path = useLocation({ select: (l) => l.pathname });
  const active = (to: string) =>
    to === "/admin"
      ? path === "/admin" || path === "/admin/" || path.startsWith("/admin/overview")
      : path.startsWith(to);
  return (
    <SidebarGroup>
      <SidebarGroupLabel className="font-mono text-[0.7rem] tracking-[0.18em] uppercase">
        {"// manage"}
      </SidebarGroupLabel>
      <SidebarMenu>
        {adminLinks.map((l) => (
          <NavLink
            key={l.to}
            link={{ to: l.to }}
            label={l.label}
            icon={l.icon}
            active={active(l.to)}
          />
        ))}
      </SidebarMenu>
    </SidebarGroup>
  );
}

// subShown is how many nodes or servers the sidebar lists under their
// section; the rest are on the section's page.
const subShown = 8;

// SubItem is a node or server listed under its section.
type SubItem = {
  id: string;
  name: string;
  link: Pick<LinkProps, "to" | "params">;
  active: boolean;
  // The status dot's color, and what it means for screen readers.
  dot: string;
  status: string;
};

// shownItems is the first few, with the one being looked at always in.
function shownItems(list: SubItem[]): SubItem[] {
  const open = list.findIndex((x) => x.active);
  return list.filter((_, i) => i < subShown - (open >= subShown ? 1 : 0) || i === open);
}

// useOpen is whether a section's list is open, remembered in the browser
// (open unless closed before).
function useOpen(key: string): [boolean, (open: boolean) => void] {
  const storageKey = `raptor.sidebar.${key}`;
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem(storageKey) !== "closed";
    } catch {
      return true;
    }
  });
  return [
    open,
    (next) => {
      setOpen(next);
      try {
        localStorage.setItem(storageKey, next ? "open" : "closed");
      } catch {
        // Private browsing: it stays as it is for this page.
      }
    },
  ];
}

// NavSection is a section link with its nodes or servers under it, which
// fold away with the chevron beside it.
function NavSection({
  id,
  link,
  label,
  icon,
  active,
  items,
  noun,
}: {
  id: string;
  link: Pick<LinkProps, "to" | "params">;
  label: string;
  icon: LucideIcon;
  active: boolean;
  items: SubItem[];
  noun: string;
}) {
  const [open, setOpen] = useOpen(id);
  const listId = `sidebar-${id}`;
  return (
    <NavLink link={link} label={label} icon={icon} active={active}>
      {items.length > 0 && (
        <>
          <SidebarMenuAction
            aria-label={open ? `Hide ${noun}s` : `Show ${noun}s`}
            aria-expanded={open}
            aria-controls={listId}
            onClick={() => setOpen(!open)}
          >
            <ChevronRight className={cn("transition-transform", open && "rotate-90")} />
          </SidebarMenuAction>
          {open && (
            <SidebarMenuSub id={listId}>
              {shownItems(items).map((x) => (
                <SidebarMenuSubItem key={x.id}>
                  <SidebarMenuSubButton isActive={x.active} render={<Link {...x.link} />}>
                    <span className={cn("size-2 shrink-0 rounded-full", x.dot)} />
                    <span className="truncate">{x.name}</span>
                    <span className="sr-only">({x.status})</span>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              ))}
              {items.length > subShown && (
                <SidebarMenuSubItem>
                  <SidebarMenuSubButton
                    className="text-muted-foreground"
                    render={<Link {...link} />}
                  >
                    <span className="truncate">
                      All {items.length} {noun}s
                    </span>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              )}
            </SidebarMenuSub>
          )}
        </>
      )}
    </NavLink>
  );
}

const toneDots: Record<ServerStatus["tone"], string> = {
  live: "bg-ok",
  pending: "bg-warn",
  failed: "bg-destructive",
  stale: "bg-muted-foreground/40",
  stopped: "bg-muted-foreground/40",
};

// OrgNav is the org's sections, with its nodes under Nodes and its servers
// under Servers: live ones first, then by name.
function OrgNav({ orgId }: { orgId: string }) {
  const path = useLocation({ select: (l) => l.pathname });
  const nodes = useOrgNodes(orgId);
  const { servers } = useOrgServers(orgId, 15_000);
  const base = `/orgs/${shortId(orgId)}`;
  const active = (p: string) =>
    p === base ? path === base || path === `${base}/` : path.startsWith(p);
  const params = { orgId };
  const byLiveThenName = (a: SubItem & { live: boolean }, b: SubItem & { live: boolean }) =>
    Number(b.live) - Number(a.live) || a.name.localeCompare(b.name);

  const nodeItems = (nodes.data?.nodes ?? [])
    .map((n) => ({
      id: n.id,
      name: n.name,
      link: { to: "/orgs/$orgId/nodes/$nodeId" as const, params: { orgId, nodeId: n.id } },
      active: path.startsWith(`${base}/nodes/${shortId(n.id)}`),
      dot: n.connected ? "bg-ok" : "bg-muted-foreground/40",
      status: n.connected ? "connected" : "offline",
      live: n.connected,
    }))
    .sort(byLiveThenName);
  const serverItems = servers
    .map((s) => {
      const st = serverStatus(s, s.node.connected);
      return {
        id: s.id,
        name: s.name || s.id.slice(-8),
        link: {
          to: "/orgs/$orgId/servers/$serverId" as const,
          params: { orgId, serverId: s.id },
        },
        active: path.startsWith(`${base}/servers/${shortId(s.id)}`),
        dot: toneDots[st.tone],
        status: st.label.toLowerCase(),
        live: st.tone === "live",
      };
    })
    .sort(byLiveThenName);

  return (
    <SidebarGroup>
      <SidebarGroupLabel className="font-mono text-[0.7rem] tracking-[0.18em] uppercase">
        {"// manage"}
      </SidebarGroupLabel>
      <SidebarMenu>
        <NavLink
          link={{ to: "/orgs/$orgId", params }}
          label="Overview"
          icon={LayoutDashboard}
          active={active(base)}
        />
        <NavSection
          id="nodes"
          link={{ to: "/orgs/$orgId/nodes", params }}
          label="Nodes"
          icon={Server}
          active={active(`${base}/nodes`)}
          items={nodeItems}
          noun="node"
        />
        <NavSection
          id="servers"
          link={{ to: "/orgs/$orgId/servers", params }}
          label="Servers"
          icon={Gamepad2}
          active={active(`${base}/servers`)}
          items={serverItems}
          noun="server"
        />
        <NavLink
          link={{ to: "/orgs/$orgId/eggs", params }}
          label="Eggs"
          icon={Egg}
          active={active(`${base}/eggs`)}
        />
        <NavLink
          link={{ to: "/orgs/$orgId/settings", params }}
          label="Settings"
          icon={Settings}
          active={active(`${base}/settings`)}
        />
      </SidebarMenu>
    </SidebarGroup>
  );
}

// NodeCount is how many of the org's nodes are connected.
function NodeCount({ orgId }: { orgId: string }) {
  const nodes = useOrgNodes(orgId);
  const list = nodes.data?.nodes ?? [];
  if (list.length === 0) return null;
  const online = list.filter((n) => n.connected).length;
  return (
    <p className="px-2 font-mono text-xs uppercase tabular-nums group-data-[collapsible=icon]:hidden">
      <span
        className={cn(
          online > 0
            ? "animate-[live-pulse_1.4s_ease-in-out_infinite] text-ok motion-reduce:animate-none"
            : "text-muted-foreground",
        )}
      >
        [ {online} online ]
      </span>
      <span className="text-muted-foreground">
        {" "}
        / {list.length} {list.length === 1 ? "node" : "nodes"}
      </span>
    </p>
  );
}

function UserMenu({ session }: { session: GetSessionResponse }) {
  const navigate = useNavigate();
  const side = useMenuSide("top");
  const signOut = useMutation(AuthService.method.signOut);
  const email = session.user?.email ?? "";
  const name = session.user?.name ?? "";
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton size="lg" tooltip={name || email}>
                <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold">
                  {initials(name || email)}
                </span>
                <span className="grid min-w-0 flex-1 text-left leading-tight">
                  {name && <span className="truncate font-medium">{name}</span>}
                  <span className={cn("truncate", name && "text-xs text-muted-foreground")}>
                    {email}
                  </span>
                </span>
                <ChevronsUpDown className="ml-auto text-muted-foreground" />
              </SidebarMenuButton>
            }
          />
          <DropdownMenuContent align="start" side={side} className="w-64">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="grid leading-tight">
                {name && <span className="truncate font-medium text-foreground">{name}</span>}
                <span className="truncate">{email}</span>
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => navigate({ to: "/settings" })}>
              <UserRound /> Account settings
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={async () => {
                await signOut.mutateAsync({});
                await navigate({ to: "/signin" });
              }}
            >
              <LogOut /> Sign out
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
