import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { BadgeCheck, Clock, ExternalLink, Loader2, Plus, Search, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { CopyButton } from "@/components/address";
import { useConfirm } from "@/components/confirm";
import { EggIcon } from "@/components/eggs/library";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { CatalogEgg } from "@/gen/raptor/panel/v1/catalog_pb";
import { DemoService, type Sandbox } from "@/gen/raptor/panel/v1/demo_pb";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { countdown, demoEggs, liveSandbox, sandboxPollMs, sandboxStatus } from "@/lib/demo";
import { popularity } from "@/lib/egg-sort";
import { categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { docs } from "@/lib/help";
import { demoClient } from "@/lib/transport";

// Guests' sandboxes in the public demo (#449, docs/PANEL.md#sandboxes): a
// server of their own from a library egg, for 15 minutes (once 15 more),
// which the demo node deletes itself when the time is up.

const demoDocs = docs("/start/demo/");

// useSandbox is the caller's sandbox in a demo org, polled while it's
// waiting or installing.
export function useSandbox(orgId: string, enabled = true) {
  return useQuery(
    DemoService.method.getSandbox,
    { orgId },
    {
      enabled: enabled && !!orgId,
      refetchInterval: (q) => sandboxPollMs(q.state.data?.sandbox?.state),
    },
  );
}

// useNow is the time, ticking every second while on.
function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

// useSandboxActions extends and deletes the caller's sandbox.
function useSandboxActions(orgId: string) {
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return {
    busy,
    error,
    extend: () => run(() => demoClient.extendSandbox({ orgId })),
    remove: async (s: Sandbox) => {
      const ok = await confirm({
        title: s.state === "queued" ? "Leave the queue?" : `Delete your ${s.eggName} server?`,
        description:
          s.state === "queued"
            ? "Someone else gets your place."
            : "Its files go with it. You can make another one afterwards.",
        confirm: s.state === "queued" ? "Leave" : "Delete",
        destructive: true,
      });
      if (ok) await run(() => demoClient.deleteSandbox({ orgId }));
    },
  };
}

// SandboxBar is the caller's sandbox in the demo banner: where it is in the
// queue, its install, or the time it has left, with its address.
export function SandboxBar({ orgId }: { orgId: string }) {
  const res = useSandbox(orgId);
  const s = res.data?.sandbox;
  const ready = s?.state === "ready";
  const now = useNow(ready);
  const act = useSandboxActions(orgId);
  if (!s || !liveSandbox(s.state)) return null;
  const deadline = s.deadline ? Number(s.deadline.seconds) * 1000 : 0;
  return (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
      {ready ? (
        <>
          <Link
            to="/orgs/$orgId/servers/$serverId"
            params={{ orgId, serverId: s.serverId }}
            className="font-medium underline-offset-3 hover:underline"
          >
            {s.eggName}
          </Link>
          <span className="inline-flex items-center gap-1 tabular-nums" aria-live="off">
            <Clock className="size-3.5" /> {countdown(deadline, now)} left
          </span>
          {s.address && (
            <span className="inline-flex items-center gap-0.5 text-muted-foreground">
              <code className="text-xs">{s.address}</code>
              <CopyButton value={s.address} />
            </span>
          )}
          {s.canExtend && (
            <Button size="xs" variant="outline" disabled={act.busy} onClick={act.extend}>
              <Plus /> 15 min
            </Button>
          )}
        </>
      ) : (
        <span className="inline-flex items-center gap-1.5">
          <Loader2 className="size-3.5 animate-spin" /> {sandboxStatus(s)}
        </span>
      )}
      <Button
        size="xs"
        variant="ghost"
        aria-label={s.state === "queued" ? "Leave the queue" : "Delete your sandbox"}
        disabled={act.busy}
        onClick={() => act.remove(s)}
      >
        <Trash2 />
      </Button>
      {act.error && <span className="text-destructive">{act.error}</span>}
    </span>
  );
}

// SandboxCard is the caller's live sandbox on the Make a server page.
function SandboxCard({ orgId, s }: { orgId: string; s: Sandbox }) {
  const ready = s.state === "ready";
  const now = useNow(ready);
  const act = useSandboxActions(orgId);
  const deadline = s.deadline ? Number(s.deadline.seconds) * 1000 : 0;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Your {s.eggName} server</CardTitle>
        <CardDescription>
          {ready
            ? `It's yours for ${countdown(deadline, now)} more. The demo node deletes it when the time is up.`
            : sandboxStatus(s)}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {s.address && (
          <p className="flex flex-wrap items-center gap-1">
            Join from your game at <code>{s.address}</code>
            <CopyButton value={s.address} />
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          {s.serverId && (
            <Link
              to="/orgs/$orgId/servers/$serverId"
              params={{ orgId, serverId: s.serverId }}
              className={buttonVariants({ size: "sm" })}
            >
              <ExternalLink /> Open it
            </Link>
          )}
          {s.canExtend && (
            <Button size="sm" variant="outline" disabled={act.busy} onClick={act.extend}>
              <Plus /> 15 more minutes
            </Button>
          )}
          <Button size="sm" variant="outline" disabled={act.busy} onClick={() => act.remove(s)}>
            <Trash2 /> {s.state === "queued" ? "Leave the queue" : "Delete it"}
          </Button>
        </div>
        {act.error && <p className="text-destructive">{act.error}</p>}
      </CardContent>
    </Card>
  );
}

// DemoNewServer is Make a server in the demo: the library's eggs that fit
// a sandbox, the rest marked too big, and the caller's sandbox once they
// have one.
export function DemoNewServer({ orgId }: { orgId: string }) {
  const catalog = useQuery(EggService.method.listEggs, { orgId });
  const demo = useQuery(DemoService.method.listDemoEggs, { orgId });
  const sandbox = useSandbox(orgId);
  const client = useQueryClient();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<CatalogEgg | null>(null);
  const [eula, setEula] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return demoEggs(catalog.data?.eggs ?? [], demo.data?.eggs ?? [], popularity).filter(
      (r) => !q || r.egg.name.toLowerCase().includes(q) || r.egg.category.includes(q),
    );
  }, [catalog.data, demo.data, query]);

  const live = sandbox.data?.sandbox;
  if (live && liveSandbox(live.state)) return <SandboxCard orgId={orgId} s={live} />;

  async function make(e: CatalogEgg) {
    setBusy(true);
    setError("");
    try {
      await demoClient.createSandbox({ orgId, eggId: e.id, acceptEula: eula });
      await client.invalidateQueries();
      setPicked(null);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const d = demo.data;
  const ended = live && !liveSandbox(live.state) && live.state === "failed" ? live : undefined;

  if (picked) {
    const needsEula = picked.features.includes("eula");
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <EggIcon category={picked.category} icon={picked.icon} />
            {picked.name}
          </CardTitle>
          <CardDescription>
            {picked.description.length > 280
              ? `${picked.description.slice(0, 280)}…`
              : picked.description}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 text-sm">
          <p className="text-muted-foreground">
            Your own server for 15 minutes from when it's installed, once 15 more if nobody's
            waiting. One core, 1.5 GB of memory, and a 10 GB disk. Players join from their game.
          </p>
          {needsEula && (
            <Label className="flex items-start gap-2 font-normal">
              <Checkbox
                checked={eula}
                onCheckedChange={(c) => setEula(c === true)}
                className="mt-0.5"
              />
              I accept the game's EULA
            </Label>
          )}
          {error && <p className="text-destructive">{error}</p>}
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => setPicked(null)}>
              Back
            </Button>
            <Button disabled={busy || (needsEula && !eula)} onClick={() => make(picked)}>
              {busy && <Loader2 className="animate-spin" />}
              Make my sandbox
            </Button>
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Try a game</CardTitle>
        <CardDescription>
          Pick one from Raptor's egg library: you get a server of your own for 15 minutes, to
          install, start, join, and change.{" "}
          {d?.open &&
            `${d.running} of ${d.maxSandboxes} running${d.queued ? `, ${d.queued} waiting` : ""}.`}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {d && !d.open && (
          <p className="text-sm text-muted-foreground">
            Sandboxes aren't open in the demo right now. The showcase servers are still yours to
            try.
          </p>
        )}
        {ended && <p className="text-sm text-destructive">{sandboxStatus(ended)}</p>}
        <div className="relative">
          <Search className="absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            aria-label="Search games"
            placeholder="Search"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        {(catalog.error || demo.error) && (
          <p className="text-sm text-destructive">{message(catalog.error ?? demo.error)}</p>
        )}
        {(catalog.isPending || demo.isPending) && (
          <p className="text-sm text-muted-foreground">Loading games…</p>
        )}
        <div className="grid gap-2 sm:grid-cols-2">
          {rows.map(({ egg: e, fits, why }) => (
            <button
              type="button"
              key={e.id}
              disabled={!fits || !d?.open}
              onClick={() => {
                setEula(false);
                setError("");
                setPicked(e);
              }}
              className="flex items-center gap-3 rounded-lg border p-3 text-left transition-colors enabled:hover:bg-muted/50 disabled:opacity-60"
            >
              <EggIcon category={e.category} icon={e.icon} />
              <span className="flex min-w-0 flex-col items-start gap-1">
                <span className="flex items-center gap-1.5 font-medium">
                  {e.name}
                  {e.certified && (
                    <BadgeCheck className="size-4 text-muted-foreground" aria-label="Certified" />
                  )}
                  {!fits && <Badge variant="outline">Too big for the demo</Badge>}
                </span>
                <span className="text-xs text-muted-foreground">
                  {fits ? categoryName(e.category) : why}
                </span>
              </span>
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          Big games (large Steam downloads, or ones needing an account) run fine on your own
          machine.{" "}
          <a href={demoDocs} target="_blank" rel="noopener" className="underline">
            About the demo
          </a>
        </p>
      </CardContent>
    </Card>
  );
}
