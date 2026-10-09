import { useQuery } from "@tanstack/react-query";
import { type ComponentType, type ReactNode, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { message } from "@/lib/errors";
import { cn } from "@/lib/utils";

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
  // Under the time: the device, the IP.
  details?: string[];
};

// A page of events, for a page token ("" first): the events and the next
// page's token ("" for none).
export type TimelinePage<E> = { events: E[]; next: string };

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

// ActivityCard is a titled card of every page of events, with "Show more".
export function ActivityCard<E>({
  title = "Activity",
  description,
  queryKey,
  fetchPage,
  toItem,
  empty = "Nothing yet.",
  action,
  refetchInterval,
}: {
  title?: string;
  description: string;
  queryKey: unknown[];
  fetchPage: (token: string) => Promise<TimelinePage<E>>;
  toItem: (e: E) => TimelineItem;
  empty?: string;
  // Beside the title: a filter, say.
  action?: ReactNode;
  // How often the first page refreshes, for events that change.
  refetchInterval?: number;
}) {
  const [pages, setPages] = useState<string[]>([""]);
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
        {pages.map((token, i) => (
          <Page
            key={token || "first"}
            token={token}
            last={i === pages.length - 1}
            queryKey={queryKey}
            fetchPage={fetchPage}
            toItem={toItem}
            empty={empty}
            refetchInterval={token ? undefined : refetchInterval}
            onMore={(t) => setPages([...pages, t])}
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
  onMore,
}: {
  token: string;
  last: boolean;
  queryKey: unknown[];
  fetchPage: (token: string) => Promise<TimelinePage<E>>;
  toItem: (e: E) => TimelineItem;
  empty: string;
  refetchInterval?: number;
  onMore: (t: string) => void;
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
      <Timeline items={page.events.map(toItem)} keyPrefix={token} />
      {last && page.next && (
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

export function Timeline({ items, keyPrefix = "" }: { items: TimelineItem[]; keyPrefix?: string }) {
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
                    {item.actor && <span className="font-medium">{item.actor} </span>}
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
