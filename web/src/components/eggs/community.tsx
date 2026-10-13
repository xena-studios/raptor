import { useInfiniteQuery, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  Flag,
  Loader2,
  Plus,
  Search,
  Share2,
  ShieldAlert,
  TriangleAlert,
  Users,
} from "lucide-react";
import { type ReactNode, useDeferredValue, useState } from "react";

import { EmptyState } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { EggService, type GetEggDetailsResponse } from "@/gen/raptor/panel/v1/egg_pb";
import { categories, categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { eggClient } from "@/lib/transport";

import { EggCard, type Library } from "./library";
import { field, select } from "./variable-dialog";

// Community eggs (docs/PANEL.md#community-eggs): eggs orgs share with
// everyone on Raptor. Adding one copies it into the org after a review of
// what it runs; anyone can report one, and staff can take one out.

// CommunityList is the community's eggs: searched on the server, most
// added first or newest.
export function CommunityList({ lib }: { lib: Library }) {
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const [sort, setSort] = useState("popular");
  const q = useDeferredValue(query.trim());
  const list = useInfiniteQuery(
    EggService.method.listCommunityEggs,
    { orgId: lib.orgId, query: q, category, sort, pageToken: "" },
    { pageParamKey: "pageToken", getNextPageParam: (p) => p.nextPageToken || undefined },
  );
  const eggs = list.data?.pages.flatMap((p) => p.eggs) ?? [];
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:max-w-xs">
          <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            type="search"
            aria-label="Search community eggs"
            placeholder="Search community eggs…"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <select
          aria-label="Category"
          className={select.replace("w-full", "w-44")}
          value={category}
          onChange={(e) => setCategory(e.target.value)}
        >
          <option value="">All categories</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              {categoryName(c)}
            </option>
          ))}
        </select>
        <select
          aria-label="Sort"
          className={`${select.replace("w-full", "w-36")} ml-auto`}
          value={sort}
          onChange={(e) => setSort(e.target.value)}
        >
          <option value="popular">Most added</option>
          <option value="new">Newest</option>
        </select>
      </div>
      <p className="-mt-2 text-xs text-muted-foreground">
        Shared by other orgs, not checked by Raptor. You see what each one runs before adding it.
      </p>
      {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
      {list.data && eggs.length === 0 && (
        <EmptyState
          icon={Users}
          title={q || category ? "No eggs match" : "Nothing shared yet"}
          description="Orgs share their published eggs from the egg's page."
        />
      )}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {eggs.map((e) => (
          <EggCard key={e.id} lib={lib} egg={e} />
        ))}
      </div>
      {list.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          disabled={list.isFetchingNextPage}
          onClick={() => list.fetchNextPage()}
        >
          {list.isFetchingNextPage && <Loader2 className="animate-spin" />} Show more
        </Button>
      )}
    </>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-sm font-medium">{title}</h3>
      {children}
    </section>
  );
}

// AddEggButton adds a community egg to the org, as it is, once the admin
// has seen what it runs and says they trust it.
export function AddEggButton({ lib, d }: { lib: Library; d: GetEggDetailsResponse }) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [trusted, setTrusted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const egg = d.egg;
  const spec = d.spec;
  if (!egg || !spec) return null;
  async function add() {
    setBusy(true);
    setError("");
    try {
      const res = await eggClient.customizeEgg({ orgId: lib.orgId, id: egg?.id ?? "", asIs: true });
      await client.invalidateQueries();
      navigate(lib.detail(res.id));
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus /> Add to my eggs
      </Button>
      <Dialog open={open} onOpenChange={(o) => !busy && setOpen(o)}>
        <DialogContent className="sm:max-w-3xl">
          <div className="flex max-h-[80vh] min-w-0 flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Add "{egg.name}"</DialogTitle>
              <DialogDescription>
                Shared by {egg.publisher || "another org"}. Your org gets its own copy, ready to
                deploy: their later changes don't reach it.
              </DialogDescription>
            </DialogHeader>
            <div className="-mx-1 flex min-w-0 flex-col gap-4 overflow-y-auto px-1">
              <Alert>
                <ShieldAlert />
                <AlertTitle>An egg is a program that runs on your nodes</AlertTitle>
                <AlertDescription>
                  Raptor doesn't check community eggs. Its install script runs as root in a
                  container with the server's files and the internet, and its images run the server.
                  Add it only if you trust what it does.
                </AlertDescription>
              </Alert>
              {d.warnings.length > 0 && (
                <ul className="flex flex-col gap-2">
                  {d.warnings.map((w) => (
                    <li
                      key={w.kind + w.text}
                      className="flex gap-2 rounded-lg border border-warn/30 bg-warn-wash p-2.5 text-sm"
                    >
                      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
                      <span>{w.text}</span>
                    </li>
                  ))}
                </ul>
              )}
              <Section title="Images">
                <ul className="flex flex-col gap-1">
                  {spec.images.map((i) => (
                    <li key={i.name} className="flex flex-wrap items-baseline gap-2 text-sm">
                      <span className="text-muted-foreground">{i.name}</span>
                      <code className="break-all">{i.ref}</code>
                    </li>
                  ))}
                </ul>
              </Section>
              <Section title="Install script">
                <p className="text-xs text-muted-foreground">
                  Runs in {spec.installContainer || "no container"}
                  {spec.installEntrypoint ? ` with ${spec.installEntrypoint}` : ""}.
                </p>
                <pre className="max-h-72 overflow-auto terminal rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre">
                  {spec.installScript || "(none)"}
                </pre>
              </Section>
              <Section title="Startup command">
                <pre className="overflow-x-auto terminal rounded-lg p-3 font-mono text-xs break-all whitespace-pre-wrap">
                  {spec.startup[0]?.command}
                </pre>
              </Section>
            </div>
            <Label className="flex items-start gap-2 text-sm font-normal">
              <Checkbox
                checked={trusted}
                onCheckedChange={(c) => setTrusted(c === true)}
                className="mt-0.5"
              />
              I've looked at what this egg runs, and I trust it on this org's nodes.
            </Label>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button disabled={busy || !trusted} onClick={add}>
                {busy && <Loader2 className="animate-spin" />} Add to my eggs
              </Button>
            </DialogFooter>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

const reasons = [
  ["malware", "It does something harmful"],
  ["broken", "It doesn't work"],
  ["spam", "Spam or a copy of another egg"],
  ["other", "Something else"],
] as const;

// ReportButton tells Raptor's staff something's wrong with a community egg.
export function ReportButton({ lib, id }: { lib: Library; id: string }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("malware");
  const [details, setDetails] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [sent, setSent] = useState(false);
  async function send() {
    setBusy(true);
    setError("");
    try {
      await eggClient.reportEgg({ orgId: lib.orgId, id, reason, details });
      setSent(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button variant="ghost" onClick={() => setOpen(true)}>
        <Flag /> Report
      </Button>
      <Dialog open={open} onOpenChange={(o) => !busy && setOpen(o)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Report this egg</DialogTitle>
            <DialogDescription>
              Raptor's staff look at every report, and can take the egg out of the community. Its
              org isn't told who reported it.
            </DialogDescription>
          </DialogHeader>
          {sent ? (
            <p className="text-sm">Thanks. Raptor's staff will look at it.</p>
          ) : (
            <div className="flex flex-col gap-4">
              <div className="grid gap-1.5">
                <Label htmlFor="report-reason">What's wrong</Label>
                <select
                  id="report-reason"
                  className={select}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                >
                  {reasons.map(([r, label]) => (
                    <option key={r} value={r}>
                      {label}
                    </option>
                  ))}
                </select>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="report-details">Details</Label>
                <textarea
                  id="report-details"
                  rows={4}
                  maxLength={1000}
                  className={field}
                  placeholder="What you saw, and where (the install script, an image, …)."
                  value={details}
                  onChange={(e) => setDetails(e.target.value)}
                />
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {sent ? "Close" : "Cancel"}
            </Button>
            {!sent && (
              <Button disabled={busy} onClick={send}>
                {busy && <Loader2 className="animate-spin" />} Report
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// SharingCard shares an org's published egg with the community.
export function SharingCard({ lib, d }: { lib: Library; d: GetEggDetailsResponse }) {
  const client = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const egg = d.egg;
  if (!egg || !lib.orgId) return null;
  const shared = !!egg.sharedAt && !egg.removed;
  async function set(on: boolean) {
    setBusy(true);
    setError("");
    try {
      await eggClient.shareEgg({ orgId: lib.orgId, id: egg?.id ?? "", shared: on });
      await client.invalidateQueries();
      setConfirm(false);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <CardTitle className="flex items-center justify-between gap-2">
          Community
          {shared && <Badge>Shared</Badge>}
          {egg.removed && <Badge variant="destructive">Removed by Raptor</Badge>}
        </CardTitle>
        <CardDescription>
          Share the egg with everyone on Raptor. Others see its file and add their own copies; your
          later edits don't change theirs.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {egg.removed ? (
          <Alert variant="destructive">
            <ShieldAlert />
            <AlertTitle>Raptor's staff took it out of the community</AlertTitle>
            <AlertDescription>
              {egg.removedReason} It stays in your eggs, and can't be shared again.
            </AlertDescription>
          </Alert>
        ) : shared ? (
          <div className="flex flex-wrap items-center gap-3">
            <p className="text-sm text-muted-foreground">
              Shared {when(egg.sharedAt)}.{" "}
              {egg.copies === 1 ? "1 org added it." : `${egg.copies} orgs added it.`}
            </p>
            <Button
              variant="outline"
              className="ml-auto"
              disabled={busy}
              onClick={() => set(false)}
            >
              Stop sharing
            </Button>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-3">
            <p className="text-sm text-muted-foreground">
              {egg.status === "published" ? "Not shared." : "Publish it first."}
            </p>
            <Button
              variant="outline"
              className="ml-auto"
              disabled={busy || egg.status !== "published"}
              onClick={() => setConfirm(true)}
            >
              <Share2 /> Share
            </Button>
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      <Dialog open={confirm} onOpenChange={(o) => !busy && setConfirm(o)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Share {egg.name}?</DialogTitle>
            <DialogDescription>
              Everyone on Raptor can see the whole egg file, with your org's name on it. Check it
              has no passwords, tokens, or private links in its defaults or scripts.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => setConfirm(false)}>
              Cancel
            </Button>
            <Button disabled={busy} onClick={() => set(true)}>
              {busy && <Loader2 className="animate-spin" />} Share
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

// ModerationCard is staff's controls for a community egg.
export function ModerationCard({ d }: { d: GetEggDetailsResponse }) {
  const client = useQueryClient();
  const withReauth = useReauth();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const egg = d.egg;
  if (!egg) return null;
  async function act(action: string) {
    setBusy(action);
    setError("");
    try {
      await withReauth(() => eggClient.moderateEgg({ id: egg?.id ?? "", action, reason }));
      await client.invalidateQueries();
      setReason("");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Moderation</CardTitle>
        <CardDescription>
          Shared by {egg.publisher}. Taking it out hides it from the community and closes its
          reports; the org keeps the egg and sees why. Copies orgs already added stay theirs.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {egg.removed ? (
          <div className="flex flex-wrap items-center gap-3">
            <p className="text-sm">Removed: {egg.removedReason}</p>
            <Button
              variant="outline"
              className="ml-auto"
              disabled={!!busy}
              onClick={() => act("restore")}
            >
              {busy === "restore" && <Loader2 className="animate-spin" />} Put it back
            </Button>
          </div>
        ) : (
          <>
            <Input
              aria-label="Why it's removed"
              placeholder="Why (the org sees this)"
              maxLength={500}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
            <div className="flex flex-wrap gap-2">
              <Button
                variant="destructive"
                disabled={!!busy || !reason.trim()}
                onClick={() => act("remove")}
              >
                {busy === "remove" && <Loader2 className="animate-spin" />} Take it out
              </Button>
              <Button variant="outline" disabled={!!busy} onClick={() => act("dismiss")}>
                {busy === "dismiss" && <Loader2 className="animate-spin" />} Dismiss reports
              </Button>
            </div>
          </>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

const reasonNames: Record<string, string> = {
  malware: "Harmful",
  broken: "Broken",
  spam: "Spam",
  other: "Other",
};

// EggReports lists open reports, for staff.
export function EggReports() {
  const list = useQuery(EggService.method.listEggReports, {});
  if (list.error) return <p className="text-sm text-destructive">{message(list.error)}</p>;
  if (list.data?.reports.length === 0) {
    return <EmptyState icon={Flag} title="No open reports" />;
  }
  return (
    <Card className="py-0">
      <ul className="divide-y">
        {list.data?.reports.map((r) => (
          <li key={r.id} className="flex flex-col gap-1 px-4 py-3">
            <div className="flex flex-wrap items-center gap-2">
              <Link
                to="/admin/eggs/$eggId"
                params={{ eggId: r.eggId }}
                className="font-medium hover:underline"
              >
                {r.eggName}
              </Link>
              <span className="text-sm text-muted-foreground">by {r.publisher}</span>
              <Badge variant={r.reason === "malware" ? "destructive" : "outline"}>
                {reasonNames[r.reason]}
              </Badge>
              {r.removed && <Badge variant="secondary">Removed</Badge>}
              <span className="ml-auto text-xs text-muted-foreground">
                {r.reporterEmail} · {when(r.createdAt)}
              </span>
            </div>
            {r.details && <p className="text-sm whitespace-pre-line">{r.details}</p>}
          </li>
        ))}
      </ul>
    </Card>
  );
}
