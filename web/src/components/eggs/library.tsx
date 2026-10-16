import { Link, type LinkProps } from "@tanstack/react-router";
import { cn } from "cn";
import {
  AppWindow,
  BadgeCheck,
  Bot,
  Database,
  Egg,
  Gamepad2,
  Joystick,
  type LucideIcon,
  Mic,
  Pickaxe,
} from "lucide-react";
import { useState } from "react";

import { LinkRow } from "@/components/entity-list";
import { EmptyState, ListToolbar, type ListView } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CatalogEgg } from "@/gen/raptor/panel/v1/catalog_pb";
import { categories, categoryName, originNames, sortEggs, statusNames, usage } from "@/lib/eggs";

// The egg library's two places: an org's Eggs page, and the admin panel's
// official library. The pages are the same; links and wording differ.
export type Library = {
  // Empty: the official library.
  orgId: string;
  list: Pick<LinkProps, "to" | "params">;
  detail: (id: string) => Pick<LinkProps, "to" | "params">;
  edit: (id: string) => Pick<LinkProps, "to" | "params">;
  create: Pick<LinkProps, "to" | "params">;
};

export function orgLibrary(orgId: string): Library {
  return {
    orgId,
    list: { to: "/orgs/$orgId/eggs", params: { orgId } },
    detail: (eggId) => ({ to: "/orgs/$orgId/eggs/$eggId", params: { orgId, eggId } }),
    edit: (eggId) => ({ to: "/orgs/$orgId/eggs/$eggId/edit", params: { orgId, eggId } }),
    create: { to: "/orgs/$orgId/eggs/new", params: { orgId } },
  };
}

export const officialLibrary: Library = {
  orgId: "",
  list: { to: "/admin/eggs" },
  detail: (eggId) => ({ to: "/admin/eggs/$eggId", params: { eggId } }),
  edit: (eggId) => ({ to: "/admin/eggs/$eggId/edit", params: { eggId } }),
  create: { to: "/admin/eggs/new" },
};

const categoryIcons: Record<string, LucideIcon> = {
  minecraft: Pickaxe,
  steam: Gamepad2,
  standalone: Joystick,
  software: AppWindow,
  database: Database,
  voice: Mic,
  generic: Bot,
};

// EggIcon is an egg's category as an icon in a tile.
export function EggIcon({ category, className }: { category: string; className?: string }) {
  const Icon = categoryIcons[category] ?? Egg;
  return (
    <span
      className={cn(
        "flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground",
        className,
      )}
    >
      <Icon className="size-4.5" strokeWidth={1.75} />
    </span>
  );
}

// EggBadges says whose an egg is and whether it can be used.
export function EggBadges({
  egg,
  showOfficial = true,
}: {
  egg: CatalogEgg;
  showOfficial?: boolean;
}) {
  return (
    <>
      {egg.certified && (
        <Badge variant="secondary" className="gap-1">
          <BadgeCheck className="size-3" /> Certified
        </Badge>
      )}
      {showOfficial && !egg.certified && egg.scope === "official" && (
        <Badge variant="secondary">Official</Badge>
      )}
      {egg.scope === "community" && <Badge variant="outline">Community</Badge>}
      {egg.status !== "published" && <Badge variant="outline">{statusNames[egg.status]}</Badge>}
    </>
  );
}

function subtitle(e: CatalogEgg): string {
  return e.version > 0 ? `${categoryName(e.category)} · v${e.version}` : categoryName(e.category);
}

// EggList is the library: search, categories, and the eggs as cards or a
// table.
export function EggList({
  lib,
  eggs,
  loading,
  action,
}: {
  lib: Library;
  eggs: CatalogEgg[];
  loading: boolean;
  action?: React.ReactNode;
}) {
  const [query, setQuery] = useState("");
  const [view, setView] = useState<ListView>("grid");
  const [category, setCategory] = useState("all");
  const present = categories.filter((c) => eggs.some((e) => e.category === c));
  const active = category === "all" || present.includes(category as never) ? category : "all";
  const q = query.trim().toLowerCase();
  const list = sortEggs(eggs).filter(
    (e) =>
      (active === "all" || e.category === active) &&
      (!q ||
        e.name.toLowerCase().includes(q) ||
        e.summary.toLowerCase().includes(q) ||
        e.description.toLowerCase().includes(q)),
  );
  const own = (e: CatalogEgg) => (lib.orgId ? e.scope === "org" : e.scope === "official");
  const mine = list.filter(own);
  const raptors = list.filter((e) => !own(e));

  if (!loading && eggs.length === 0) {
    return (
      <EmptyState
        icon={Egg}
        title="No eggs yet"
        description="Make one from scratch or import a Pterodactyl or Pelican egg."
        action={action}
      />
    );
  }
  return (
    <>
      <ListToolbar
        noun="egg"
        count={list.length}
        query={query}
        onQuery={setQuery}
        view={view}
        onView={setView}
      />
      {present.length > 1 && (
        <div className="-mt-2 flex flex-wrap gap-1.5">
          {["all", ...present].map((c) => (
            <Button
              key={c}
              size="sm"
              variant={active === c ? "secondary" : "ghost"}
              aria-pressed={active === c}
              onClick={() => setCategory(c)}
            >
              {c === "all" ? "All" : categoryName(c)}
            </Button>
          ))}
        </div>
      )}
      {list.length === 0 && !loading && (
        <p className="text-sm text-muted-foreground">No eggs match.</p>
      )}
      {[
        [lib.orgId ? "Your org's eggs" : "Official eggs", mine],
        ["Raptor's eggs", raptors],
      ].map(([title, group]) =>
        (group as CatalogEgg[]).length === 0 ? null : (
          <section key={title as string} className="flex flex-col gap-3">
            {mine.length > 0 && raptors.length > 0 && (
              <h2 className="text-sm font-medium">{title as string}</h2>
            )}
            {view === "grid" ? (
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {(group as CatalogEgg[]).map((e) => (
                  <EggCard key={e.id} lib={lib} egg={e} />
                ))}
              </div>
            ) : (
              <EggTable lib={lib} eggs={group as CatalogEgg[]} />
            )}
          </section>
        ),
      )}
    </>
  );
}

export function EggCard({ lib, egg }: { lib: Library; egg: CatalogEgg }) {
  return (
    <Link
      {...lib.detail(egg.id)}
      className={cn(
        "group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
        egg.status === "archived" && "opacity-60",
      )}
    >
      <Card className="h-full transition-colors group-hover:bg-muted/40">
        <CardHeader className="flex flex-row items-start gap-3">
          <EggIcon category={egg.category} />
          <div className="min-w-0 flex-1 space-y-1">
            <CardTitle className="truncate">{egg.name}</CardTitle>
            <CardDescription className="truncate">
              {egg.publisher
                ? `${categoryName(egg.category)} · by ${egg.publisher}`
                : subtitle(egg)}
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent className="flex flex-1 flex-col gap-3">
          <p className="line-clamp-2 min-h-10 text-sm text-muted-foreground">
            {egg.summary || egg.description || "No description."}
          </p>
          <div className="mt-auto flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            <EggBadges egg={egg} showOfficial={!!lib.orgId} />
            {egg.removed && <Badge variant="destructive">Removed</Badge>}
            {egg.sharedAt && egg.scope === "org" && <Badge variant="outline">Shared</Badge>}
            <span className="ml-auto">
              {egg.scope === "community"
                ? egg.copies === 1
                  ? "Added by 1 org"
                  : `Added by ${egg.copies} orgs`
                : usage(egg.servers)}
            </span>
          </div>
        </CardContent>
      </Card>
    </Link>
  );
}

function EggTable({ lib, eggs }: { lib: Library; eggs: CatalogEgg[] }) {
  return (
    <Card className="py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Egg</TableHead>
            <TableHead className="hidden md:table-cell">Origin</TableHead>
            <TableHead className="hidden sm:table-cell">Status</TableHead>
            <TableHead className="text-right">Version</TableHead>
            <TableHead className="text-right">In use</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {eggs.map((e) => (
            <LinkRow key={e.id} link={lib.detail(e.id)}>
              <TableCell>
                <div className="flex items-center gap-3">
                  <EggIcon category={e.category} className="size-8" />
                  <div className="min-w-0">
                    <Link {...lib.detail(e.id)} className="font-medium hover:underline">
                      {e.name}
                    </Link>
                    <p className="text-xs text-muted-foreground">{categoryName(e.category)}</p>
                  </div>
                </div>
              </TableCell>
              <TableCell className="hidden text-muted-foreground md:table-cell">
                {originNames[e.origin] ?? ""}
              </TableCell>
              <TableCell className="hidden sm:table-cell">
                <span className="flex flex-wrap gap-1.5">
                  <EggBadges egg={e} showOfficial={!!lib.orgId} />
                  {e.status === "published" && !e.certified && e.scope === "org" && (
                    <span className="text-sm text-muted-foreground">Published</span>
                  )}
                </span>
              </TableCell>
              <TableCell className="text-right font-mono text-xs text-muted-foreground">
                {e.version > 0 ? `v${e.version}` : "—"}
              </TableCell>
              <TableCell className="text-right text-muted-foreground font-mono text-xs tabular-nums">
                {e.servers || "—"}
              </TableCell>
            </LinkRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
