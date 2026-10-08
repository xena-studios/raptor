import { Link, type LinkProps } from "@tanstack/react-router";
import { ChevronLeft, LayoutGrid, List, type LucideIcon, Search } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// The pieces every page is built from: a header, routed tabs, list
// toolbars, empty states, and label/value lists.

// PageHeader is a page's title, with a small label or a back link above
// it, a description under it, and actions on the right.
export function PageHeader({
  title,
  description,
  actions,
  eyebrow,
  back,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  eyebrow?: string;
  back?: { label: string } & Pick<LinkProps, "to" | "params" | "search">;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0 space-y-1">
        {back ? (
          <Link
            to={back.to}
            params={back.params}
            search={back.search}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeft className="size-4" />
            {back.label}
          </Link>
        ) : (
          eyebrow && <p className="text-sm text-muted-foreground">{eyebrow}</p>
        )}
        <h1 className="font-heading text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

// RouteTabs is a row of links to a page's sections; the router marks the
// current one (data-status="active").
export function RouteTabs({ label, children }: { label: string; children: ReactNode }) {
  return (
    <nav aria-label={label} className="-mb-px flex gap-4 overflow-x-auto border-b">
      {children}
    </nav>
  );
}

export const tabClass =
  "whitespace-nowrap border-b-2 border-transparent px-1 pb-2.5 text-sm text-muted-foreground transition-colors hover:text-foreground data-[status=active]:border-primary data-[status=active]:font-medium data-[status=active]:text-foreground";

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
}: {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
      {Icon && <Icon className="size-7 text-muted-foreground" />}
      <div className="space-y-1">
        <p className="text-sm font-medium">{title}</p>
        {description && (
          <p className="mx-auto max-w-sm text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {action}
    </div>
  );
}

export type ListView = "grid" | "list";

// ListToolbar is a list's search box, its count, and the grid/list switch.
export function ListToolbar({
  noun,
  count,
  query,
  onQuery,
  view,
  onView,
}: {
  noun: string;
  count: number;
  query: string;
  onQuery: (q: string) => void;
  view: ListView;
  onView: (v: ListView) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <div className="relative w-full sm:max-w-xs">
        <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
        <Input
          type="search"
          aria-label={`Search ${noun}s`}
          placeholder={`Search ${noun}s…`}
          className="pl-8"
          value={query}
          onChange={(e) => onQuery(e.target.value)}
        />
      </div>
      <p className="ml-auto text-sm text-muted-foreground tabular-nums">
        {count} {count === 1 ? noun : `${noun}s`}
      </p>
      <div className="flex rounded-lg border p-0.5">
        {(
          [
            ["grid", LayoutGrid, "Grid view"],
            ["list", List, "List view"],
          ] as const
        ).map(([v, Icon, label]) => (
          <Button
            key={v}
            size="icon-sm"
            variant={view === v ? "secondary" : "ghost"}
            aria-pressed={view === v}
            aria-label={label}
            className={cn(view !== v && "text-muted-foreground")}
            onClick={() => onView(v)}
          >
            <Icon />
          </Button>
        ))}
      </div>
    </div>
  );
}

// DetailList is label/value rows, for a card's facts.
export function DetailList({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="divide-y text-sm">
      {rows.map(([label, value]) => (
        <div key={label} className="flex items-baseline justify-between gap-4 py-2">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="min-w-0 truncate text-right">{value}</dd>
        </div>
      ))}
    </dl>
  );
}
