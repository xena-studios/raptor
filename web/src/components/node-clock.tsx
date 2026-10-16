import { useLocation } from "@tanstack/react-router";
import { Clock, Plus, X } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { sameId } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";
import {
  city,
  clock,
  day,
  difference,
  timeZones,
  validZone,
  wallTime,
  zoneName,
} from "@/lib/zones";

// NodeClock is the node's time, in the header of its pages and its servers'
// (servers run on the node's time zone): open it for that time in your
// zone, UTC, and zones you add, or to see what a time on the node is
// elsewhere.

const zonesKey = "raptor.clock.zones";

function savedZones(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(zonesKey) ?? "[]") as unknown;
    return Array.isArray(v)
      ? v.filter((z): z is string => typeof z === "string" && validZone(z))
      : [];
  } catch {
    return [];
  }
}

function saveZones(zones: string[]) {
  try {
    localStorage.setItem(zonesKey, JSON.stringify(zones));
  } catch {
    // Private browsing: they last until the page reloads.
  }
}

// useNow is the time, updated on the minute.
function useNow(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      setNow(new Date());
      timer = setTimeout(tick, 60_000 - (Date.now() % 60_000) + 50);
    };
    timer = setTimeout(tick, 60_000 - (Date.now() % 60_000) + 50);
    return () => clearTimeout(timer);
  }, []);
  return now;
}

// The node a path is about: a node's pages, or a server's.
const nodePath = /^\/orgs\/([^/]+)\/(?:nodes|servers)\/([0-9a-f-]+)(?:\/|$)/;

export function NodeClock({ orgId }: { orgId: string }) {
  const path = useLocation({ select: (l) => l.pathname });
  const m = nodePath.exec(path);
  const nodes = useOrgNodes(orgId);
  const node =
    m && sameId(m[1] ?? "", orgId)
      ? nodes.data?.nodes.find((n) => sameId(n.id, m[2] ?? ""))
      : undefined;
  if (!node?.timezone || !validZone(node.timezone)) return null;
  return <ZoneClock tz={node.timezone} name={node.name} />;
}

function ZoneClock({ tz, name }: { tz: string; name: string }) {
  const now = useNow();
  const mine = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const [zones, setZones] = useState(savedZones);
  const [adding, setAdding] = useState("");
  const [error, setError] = useState("");
  // A time on the node to convert ("HH:MM"); empty is now.
  const [at, setAt] = useState("");
  const shown = (() => {
    const [h, mi] = at.split(":").map(Number);
    return at && Number.isFinite(h) && Number.isFinite(mi)
      ? wallTime(tz, h ?? 0, mi ?? 0, now)
      : now;
  })();

  const rows = [
    ...(mine !== tz ? [{ zone: mine, label: `You (${city(mine)})`, removable: false }] : []),
    ...(tz !== "UTC" && mine !== "UTC" ? [{ zone: "UTC", label: "UTC", removable: false }] : []),
    ...zones
      .filter((z) => z !== tz && z !== mine && z !== "UTC")
      .map((z) => ({ zone: z, label: city(z), removable: true })),
  ];

  function add(e: FormEvent) {
    e.preventDefault();
    const z =
      timeZones().find((x) => x.toLowerCase() === adding.trim().toLowerCase()) ?? adding.trim();
    if (!validZone(z)) {
      setError(`"${adding}" isn't a time zone. Pick one from the list, like Europe/London.`);
      return;
    }
    const next = [...zones.filter((x) => x !== z), z].slice(-8);
    setZones(next);
    saveZones(next);
    setAdding("");
    setError("");
  }

  function remove(z: string) {
    const next = zones.filter((x) => x !== z);
    setZones(next);
    saveZones(next);
  }

  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto gap-1.5 font-normal text-muted-foreground tabular-nums"
            aria-label={`The node's time: ${clock(tz, now)}, ${tz}. Open to compare with other time zones.`}
          />
        }
      >
        <Clock />
        {clock(tz, now)}
        <span className="hidden sm:inline">{zoneName(tz, now)}</span>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0">
        <div className="border-b px-4 py-3">
          <p className="text-xs text-muted-foreground">
            {name}'s time · {tz}
          </p>
          <p className="text-2xl font-semibold tabular-nums">
            {clock(tz, shown)}{" "}
            <span className="text-sm font-normal text-muted-foreground">{day(tz, shown)}</span>
          </p>
          <div className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
            <label htmlFor="clock-at">Another time on the node:</label>
            <Input
              id="clock-at"
              type="time"
              value={at}
              onChange={(e) => setAt(e.target.value)}
              className="h-7 w-28 text-xs"
            />
            {at && (
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => setAt("")}
              >
                Now
              </button>
            )}
          </div>
        </div>
        <ul className="divide-y">
          {rows.map((r) => (
            <li key={r.zone} className="flex items-center gap-3 px-4 py-2 text-sm">
              <span className="min-w-0 flex-1">
                <span className="block truncate">{r.label}</span>
                <span className="block text-xs text-muted-foreground">
                  {difference(r.zone, tz, shown)} · {zoneName(r.zone, shown)}
                </span>
              </span>
              <span className="text-right tabular-nums">
                <span className="block font-medium">{clock(r.zone, shown)}</span>
                {day(r.zone, shown) !== day(tz, shown) && (
                  <span className="block text-xs text-muted-foreground">{day(r.zone, shown)}</span>
                )}
              </span>
              {r.removable && (
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Remove ${r.label}`}
                  onClick={() => remove(r.zone)}
                >
                  <X />
                </Button>
              )}
            </li>
          ))}
        </ul>
        <form onSubmit={add} className="flex gap-2 border-t px-4 py-3">
          <Input
            list="clock-zones"
            placeholder="Add a time zone"
            aria-label="Add a time zone"
            autoComplete="off"
            value={adding}
            onChange={(e) => setAdding(e.target.value)}
            className="h-8 flex-1 text-sm"
          />
          <datalist id="clock-zones">
            {timeZones().map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
          <Button type="submit" size="sm" variant="outline" disabled={!adding.trim()}>
            <Plus /> Add
          </Button>
        </form>
        {error && <p className="px-4 pb-3 text-xs text-destructive">{error}</p>}
        <p className="border-t px-4 py-2 text-xs text-muted-foreground">
          Servers on this node run on its time zone. Change it on the node's System tab.
        </p>
      </PopoverContent>
    </Popover>
  );
}
