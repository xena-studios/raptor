import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@tanstack/react-query";
import { cn } from "cn";
import { Search, X } from "lucide-react";
import { type ComponentType, type ReactNode, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { type ActivityFilter, ActivityFilterSchema } from "@/gen/raptor/panel/v1/audit_pb";
import { message } from "@/lib/errors";

// The activity pages' timeline (account, org, server): events grouped by
// day, newest first, each an icon, a sentence, and when and from where.

export type TimelineItem = {
  id: string;
  at?: Date;
  icon: ComponentType<{ className?: string }>;
  // Who did it, in bold before the sentence (org and server logs).
  actor?: string;
  text: ReactNode;
  failed?: boolean;
  badge?: ReactNode;
  // Under the time: the device.
  details?: string[];
  // Where from, and who did it (a user ID): with filters, clicking them
  // narrows the list to them.
  ip?: string;
  actorId?: string;
};

// A page of events, for a page token ("" first): the events and the next
// page's token ("" for none).
export type TimelinePage<E> = { events: E[]; next: string };

// What an activity list can be narrowed by (docs/PANEL.md#audit-log).
// Kinds are groups of actions (ActivityFilter.actions); people and places
// are the list's own (an org's members, its nodes and servers).
export type FilterOptions = {
  kinds: { label: string; actions: string[] }[];
  people?: { id: string; label: string }[];
  places?: { label: string; options: { value: string; label: string }[] };
  // Whether anything in the list can fail.
  failures?: boolean;
};

export type Filters = {
  query: string;
  range: "any" | "today" | "7d" | "30d" | "custom";
  from: string; // yyyy-mm-dd, for a custom range
  to: string;
  kind: string; // a kind's label
  person: string;
  place: string;
  ip: string;
  failed: boolean;
};

export const noFilters: Filters = {
  query: "",
  range: "any",
  from: "",
  to: "",
  kind: "",
  person: "",
  place: "",
  ip: "",
  failed: false,
};

function active(f: Filters) {
  return JSON.stringify(f) !== JSON.stringify(noFilters);
}

// toFilter makes the filters an ActivityFilter. Days are the browser's.
export function toFilter(f: Filters, options?: FilterOptions): ActivityFilter {
  const day = (s: string) => {
    const [y = 1970, m = 1, d = 1] = s.split("-").map(Number);
    return new Date(y, m - 1, d);
  };
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const ago = (days: number) => new Date(today.getTime() - days * 86_400_000);
  let from: Date | undefined;
  let to: Date | undefined;
  switch (f.range) {
    case "today":
      from = today;
      break;
    case "7d":
      from = ago(6);
      break;
    case "30d":
      from = ago(29);
      break;
    case "custom":
      if (f.from) from = day(f.from);
      if (f.to) to = new Date(day(f.to).getTime() + 86_400_000);
      break;
  }
  return create(ActivityFilterSchema, {
    query: f.query.trim(),
    from: from && timestampFromDate(from),
    to: to && timestampFromDate(to),
    ip: f.ip.trim(),
    actorId: f.person,
    actions: options?.kinds.find((k) => k.label === f.kind)?.actions ?? [],
    failed: f.failed,
  });
}

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// useDebounced is a value once it has stopped changing for a moment.
function useDebounced<T>(value: T, ms = 350): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

function FilterBar({
  value,
  onChange,
  options,
}: {
  value: Filters;
  onChange: (f: Filters) => void;
  options: FilterOptions;
}) {
  const set = (patch: Partial<Filters>) => onChange({ ...value, ...patch });
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            type="search"
            aria-label="Search the activity"
            placeholder="Search emails, names, devices…"
            className="pl-8"
            maxLength={100}
            value={value.query}
            onChange={(e) => set({ query: e.target.value })}
          />
        </div>
        <Input
          aria-label="IP address or range"
          placeholder="IP or range"
          className={cn("w-36", value.ip && "font-mono text-xs")}
          value={value.ip}
          onChange={(e) => set({ ip: e.target.value })}
        />
        <select
          aria-label="When"
          className={select}
          value={value.range}
          onChange={(e) => set({ range: e.target.value as Filters["range"] })}
        >
          <option value="any">Any time</option>
          <option value="today">Today</option>
          <option value="7d">Last 7 days</option>
          <option value="30d">Last 30 days</option>
          <option value="custom">Dates…</option>
        </select>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {value.range === "custom" && (
          <>
            <Input
              type="date"
              aria-label="From"
              className="w-auto"
              value={value.from}
              max={value.to || undefined}
              onChange={(e) => set({ from: e.target.value })}
            />
            <span className="text-xs text-muted-foreground">to</span>
            <Input
              type="date"
              aria-label="To"
              className="w-auto"
              value={value.to}
              min={value.from || undefined}
              onChange={(e) => set({ to: e.target.value })}
            />
          </>
        )}
        <select
          aria-label="Kind of event"
          className={select}
          value={value.kind}
          onChange={(e) => set({ kind: e.target.value })}
        >
          <option value="">Everything</option>
          {options.kinds.map((k) => (
            <option key={k.label} value={k.label}>
              {k.label}
            </option>
          ))}
        </select>
        {options.people && (
          <select
            aria-label="Who"
            className={cn(select, "max-w-48")}
            value={value.person}
            onChange={(e) => set({ person: e.target.value })}
          >
            <option value="">Anyone</option>
            {options.people.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        )}
        {options.places && options.places.options.length > 0 && (
          <select
            aria-label={options.places.label}
            className={cn(select, "max-w-48")}
            value={value.place}
            onChange={(e) => set({ place: e.target.value })}
          >
            <option value="">{options.places.label}: all</option>
            {options.places.options.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        )}
        {options.failures && (
          <label className="flex items-center gap-2 px-1 text-sm">
            <Checkbox checked={value.failed} onCheckedChange={(on) => set({ failed: !!on })} />
            Failed only
          </label>
        )}
        {active(value) && (
          <Button size="sm" variant="ghost" onClick={() => onChange(noFilters)}>
            <X /> Clear
          </Button>
        )}
      </div>
    </div>
  );
}

function dayLabel(d: Date): string {
  const today = new Date();
  const yesterday = new Date();
  yesterday.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    weekday: "long",
    month: "long",
    day: "numeric",
    year: d.getFullYear() === today.getFullYear() ? undefined : "numeric",
  });
}

// ActivityCard is a titled card of every page of events, with "Show more",
// and with filters if it's given what to filter by.
export function ActivityCard<E>({
  title = "Activity",
  description,
  queryKey,
  fetchPage,
  toItem,
  empty = "Nothing yet.",
  action,
  refetchInterval,
  limit,
  filters,
}: {
  title?: string;
  description: string;
  queryKey: unknown[];
  fetchPage: (token: string, filters: Filters) => Promise<TimelinePage<E>>;
  toItem: (e: E) => TimelineItem;
  empty?: string;
  // Beside the title: a filter, say.
  action?: ReactNode;
  // How often the first page refreshes, for events that change.
  refetchInterval?: number;
  // Only the newest few, with no "Show more" (an overview's card).
  limit?: number;
  filters?: FilterOptions;
}) {
  const [typed, setTyped] = useState<Filters>(noFilters);
  // What's typed is searched for once typing pauses; the rest at once.
  const words = useDebounced({ query: typed.query, ip: typed.ip });
  const applied: Filters = { ...typed, ...words };
  const key = JSON.stringify(applied);
  const [pages, setPages] = useState<{ key: string; tokens: string[] }>({ key, tokens: [""] });
  const tokens = pages.key === key ? pages.tokens : [""];
  const narrow = (patch: Partial<Filters>) => setTyped({ ...typed, ...patch });
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
        {action}
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {filters && <FilterBar value={typed} onChange={setTyped} options={filters} />}
        {tokens.map((token, i) => (
          <Page
            key={`${key}-${token || "first"}`}
            token={token}
            last={i === tokens.length - 1}
            queryKey={[...queryKey, key]}
            fetchPage={(t) => fetchPage(t, applied)}
            toItem={toItem}
            empty={filters && active(applied) ? "Nothing matches these filters." : empty}
            refetchInterval={token ? undefined : refetchInterval}
            limit={limit}
            onMore={(t) => setPages({ key, tokens: [...tokens, t] })}
            onFilter={
              filters
                ? (f) => {
                    if (f.ip) narrow({ ip: f.ip });
                    if (f.actorId && filters.people?.some((p) => p.id === f.actorId))
                      narrow({ person: f.actorId });
                  }
                : undefined
            }
          />
        ))}
      </CardContent>
    </Card>
  );
}

function Page<E>({
  token,
  last,
  queryKey,
  fetchPage,
  toItem,
  empty,
  refetchInterval,
  limit,
  onMore,
  onFilter,
}: {
  token: string;
  last: boolean;
  queryKey: unknown[];
  fetchPage: (token: string) => Promise<TimelinePage<E>>;
  toItem: (e: E) => TimelineItem;
  empty: string;
  refetchInterval?: number;
  limit?: number;
  onMore: (t: string) => void;
  onFilter?: (f: { ip?: string; actorId?: string }) => void;
}) {
  const query = useQuery({
    queryKey: [...queryKey, token],
    queryFn: () => fetchPage(token),
    refetchInterval,
  });
  if (query.error) return <p className="text-sm text-destructive">{message(query.error)}</p>;
  const page = query.data;
  if (!page) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (page.events.length === 0 && !token) {
    return <p className="text-sm text-muted-foreground">{empty}</p>;
  }
  return (
    <>
      <Timeline
        items={page.events.slice(0, limit).map(toItem)}
        keyPrefix={token}
        onFilter={onFilter}
      />
      {last && page.next && !limit && (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          onClick={() => onMore(page.next)}
        >
          Show more
        </Button>
      )}
    </>
  );
}

function Timeline({
  items,
  keyPrefix = "",
  onFilter,
}: {
  items: TimelineItem[];
  keyPrefix?: string;
  onFilter?: (f: { ip?: string; actorId?: string }) => void;
}) {
  const link = "rounded-sm underline-offset-2 hover:text-foreground hover:underline";
  const days: { label: string; items: TimelineItem[] }[] = [];
  for (const item of items) {
    const label = item.at ? dayLabel(item.at) : "";
    const lastDay = days[days.length - 1];
    if (lastDay && lastDay.label === label) lastDay.items.push(item);
    else days.push({ label, items: [item] });
  }
  return (
    <>
      {days.map((day) => (
        <section key={`${keyPrefix}-${day.label}`} className="flex flex-col gap-3">
          <h3 className="text-xs font-medium text-muted-foreground">{day.label}</h3>
          <ol>
            {day.items.map((item, i) => (
              <li key={item.id} className="relative flex gap-3 pb-4 last:pb-0">
                {i < day.items.length - 1 && (
                  <span
                    aria-hidden
                    className="absolute top-8 -bottom-0 left-4 w-px -translate-x-1/2 bg-border"
                  />
                )}
                <span
                  className={cn(
                    "relative flex size-8 shrink-0 items-center justify-center rounded-lg border bg-card",
                    item.failed ? "text-destructive" : "text-muted-foreground",
                  )}
                >
                  <item.icon className="size-4" />
                </span>
                <div className="min-w-0 flex-1 pt-1">
                  <p className={cn("text-sm", item.failed && "text-destructive")}>
                    {item.actor &&
                      (onFilter && item.actorId ? (
                        <>
                          <button
                            type="button"
                            className={cn("font-medium", link)}
                            title={`Only what ${item.actor} did`}
                            onClick={() => onFilter({ actorId: item.actorId })}
                          >
                            {item.actor}
                          </button>{" "}
                        </>
                      ) : (
                        <span className="font-medium">{item.actor} </span>
                      ))}
                    {item.text}
                    {item.badge}
                  </p>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">
                    <time title={item.at?.toLocaleString()}>
                      {item.at?.toLocaleTimeString(undefined, {
                        hour: "numeric",
                        minute: "2-digit",
                      })}
                    </time>
                    {(item.details ?? [])
                      .filter(Boolean)
                      .map((x) => ` · ${x}`)
                      .join("")}
                    {item.ip && " · "}
                    {item.ip &&
                      (onFilter ? (
                        <button
                          type="button"
                          className={cn("font-mono", link)}
                          title={`Only what came from ${item.ip}`}
                          onClick={() => onFilter({ ip: item.ip })}
                        >
                          {item.ip}
                        </button>
                      ) : (
                        <span className="font-mono">{item.ip}</span>
                      ))}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </section>
      ))}
    </>
  );
}
