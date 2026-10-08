import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  Check,
  ChevronsUpDown,
  LogOut,
  Menu,
  Plus,
  ScrollText,
  Server,
  Settings,
  Shield,
  Users,
  X,
} from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

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
import { AuthService, type GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin, roleNames } from "@/lib/format";
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

// The org page's sections, which the sidebar links to.
export type OrgSection = "nodes" | "members" | "log" | "settings";

// The frame around signed-in pages: a sidebar with the org switcher, the
// org's nodes and sections, and the user's menu; the page beside it.
export function AppShell({
  session,
  orgId,
  section,
  children,
}: {
  session: GetSessionResponse;
  // The org the page is about; otherwise the last one opened.
  orgId?: string;
  // The org page's section, to highlight.
  section?: OrgSection;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const list = orgs.data?.orgs ?? [];
  const currentId = orgId ?? lastOrg() ?? list[0]?.id;
  const current = list.find((o) => o.id === currentId) ?? list[0];

  useEffect(() => {
    if (orgId) {
      try {
        localStorage.setItem(lastOrgKey, orgId);
      } catch {
        // Private browsing: nothing to remember it in.
      }
    }
  }, [orgId]);

  const sidebar = (
    <div className="flex h-full flex-col gap-2 p-3">
      <Link
        to="/"
        className="flex items-center gap-2 px-2 py-1.5 font-heading text-lg font-semibold"
        onClick={() => setOpen(false)}
      >
        <Logo className="size-7" />
        Raptor
      </Link>
      <OrgSwitcher orgs={list} current={current?.id} />
      {current && (
        <OrgNav
          orgId={current.id}
          admin={isAdmin(current.role)}
          section={orgId === current.id ? section : undefined}
          onNavigate={() => setOpen(false)}
        />
      )}
      <div className="mt-auto">
        <UserMenu session={session} />
      </div>
    </div>
  );

  return (
    <div className="flex min-h-svh">
      <aside className="sticky top-0 hidden h-svh w-64 shrink-0 border-r bg-sidebar text-sidebar-foreground md:block">
        {sidebar}
      </aside>
      {open && (
        <div className="fixed inset-0 z-40 md:hidden">
          <button
            type="button"
            aria-label="Close the menu"
            className="absolute inset-0 bg-black/50"
            onClick={() => setOpen(false)}
          />
          <aside className="absolute inset-y-0 left-0 w-72 border-r bg-sidebar text-sidebar-foreground">
            <button
              type="button"
              aria-label="Close the menu"
              className="absolute top-3 right-3 rounded-md p-1 text-muted-foreground hover:text-foreground"
              onClick={() => setOpen(false)}
            >
              <X className="size-5" />
            </button>
            {sidebar}
          </aside>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-2 border-b px-4 py-2 md:hidden">
          <button
            type="button"
            aria-label="Open the menu"
            className="rounded-md p-1 text-muted-foreground hover:text-foreground"
            onClick={() => setOpen(true)}
          >
            <Menu className="size-5" />
          </button>
          <Logo className="size-6" />
          <span className="font-heading font-semibold">{current?.name ?? "Raptor"}</span>
        </header>
        <main className="mx-auto w-full max-w-5xl p-6">{children}</main>
      </div>
    </div>
  );
}

type OrgSummary = { id: string; name: string; role: number };

function OrgSwitcher({ orgs, current }: { orgs: OrgSummary[]; current?: string }) {
  const navigate = useNavigate();
  const org = orgs.find((o) => o.id === current);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex w-full items-center gap-2 rounded-lg border bg-background/50 px-2.5 py-2 text-left text-sm outline-none hover:bg-sidebar-accent focus-visible:ring-3 focus-visible:ring-ring/50">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-xs font-semibold text-primary-foreground">
          {initials(org?.name ?? "?")}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{org?.name ?? "No org"}</span>
          {org && (
            <span className="block text-xs text-muted-foreground">
              {roleNames[org.role as keyof typeof roleNames]}
            </span>
          )}
        </span>
        <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
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

function OrgNav({
  orgId,
  admin,
  section,
  onNavigate,
}: {
  orgId: string;
  admin: boolean;
  section?: OrgSection;
  onNavigate: () => void;
}) {
  const nodes = useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 15_000 });
  const item =
    "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground/80 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground";
  const active = "bg-sidebar-accent font-medium text-sidebar-accent-foreground";
  return (
    <nav className="flex flex-col gap-0.5" aria-label="Org">
      <Link
        to="/orgs/$orgId"
        params={{ orgId }}
        className={cn(item, section === "nodes" && active)}
        onClick={onNavigate}
      >
        <Server className="size-4" /> Nodes
      </Link>
      {(nodes.data?.nodes ?? []).map((n) => (
        <Link
          key={n.id}
          to="/orgs/$orgId/nodes/$nodeId"
          params={{ orgId, nodeId: n.id }}
          className={cn(item, "pl-8")}
          activeProps={{ className: active }}
          onClick={onNavigate}
        >
          <span
            className={cn(
              "size-2 shrink-0 rounded-full",
              n.connected ? "bg-emerald-500" : "bg-muted-foreground/40",
            )}
            title={n.connected ? "Connected" : "Offline"}
          />
          <span className="truncate">{n.name}</span>
        </Link>
      ))}
      <Link
        to="/orgs/$orgId"
        params={{ orgId }}
        search={{ section: "members" }}
        className={cn(item, section === "members" && active)}
        onClick={onNavigate}
      >
        <Users className="size-4" /> Members
      </Link>
      {admin && (
        <Link
          to="/orgs/$orgId"
          params={{ orgId }}
          search={{ section: "log" }}
          className={cn(item, section === "log" && active)}
          onClick={onNavigate}
        >
          <ScrollText className="size-4" /> Audit log
        </Link>
      )}
      {admin && (
        <Link
          to="/orgs/$orgId"
          params={{ orgId }}
          search={{ section: "settings" }}
          className={cn(item, section === "settings" && active)}
          onClick={onNavigate}
        >
          <Settings className="size-4" /> Settings
        </Link>
      )}
    </nav>
  );
}

function UserMenu({ session }: { session: GetSessionResponse }) {
  const navigate = useNavigate();
  const signOut = useMutation(AuthService.method.signOut);
  const email = session.user?.email ?? "";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-sm outline-none hover:bg-sidebar-accent focus-visible:ring-3 focus-visible:ring-ring/50">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold uppercase">
          {email.slice(0, 2)}
        </span>
        <span className="min-w-0 flex-1 truncate">{email}</span>
        <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="w-60">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="truncate">{email}</DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate({ to: "/settings/security" })}>
          <Shield className="size-4" /> Account security
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
  );
}

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  return (
    (words.length > 1 ? (words[0]?.[0] ?? "") + (words[1]?.[0] ?? "") : name.slice(0, 2)) || "?"
  ).toUpperCase();
}
