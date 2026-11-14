import { useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import { ChevronDown, FileUp, Loader2, ShieldAlert, TriangleAlert, X } from "lucide-react";
import { type DragEvent, useState } from "react";

import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import type { ReviewEggFileResponse } from "@/gen/raptor/panel/v1/egg_pb";
import { categories, categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { eggClient } from "@/lib/transport";

const maxFile = 1 << 20;
// Eggs reviewed at once.
const parallel = 4;

type Item = {
  key: string;
  label: string; // the file's name or the link
  state: "reviewing" | "ready" | "failed" | "importing" | "imported";
  error?: string;
  preview?: ReviewEggFileResponse;
  category: string;
  include: boolean;
  open?: boolean;
};

// BulkEggImport imports many eggs at once (docs/PANEL.md#importing): files
// and links, each reviewed like a single import (what it runs, and
// warnings), then, once the admin says they trust them, imported one by
// one. Eggs that say their category (x-raptor.category, as the egg library
// exports them) get it. orgId empty imports official eggs.
export function BulkEggImport({
  orgId,
  open,
  onOpenChange,
}: {
  orgId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const client = useQueryClient();
  const withReauth = useReauth();
  const [items, setItems] = useState<Item[]>([]);
  const [links, setLinks] = useState("");
  const [trusted, setTrusted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);

  const update = (key: string, next: Partial<Item>) =>
    setItems((list) => list.map((i) => (i.key === key ? { ...i, ...next } : i)));

  function reset() {
    setItems([]);
    setLinks("");
    setTrusted(false);
    setError("");
  }

  // review reads each source with the Panel's review, a few at a time.
  async function review(
    sources: { label: string; source: () => Promise<ReviewEggFileResponse> }[],
  ) {
    const added = sources.map((s, n) => ({
      key: `${Date.now()}-${n}-${s.label}`,
      label: s.label,
      state: "reviewing" as const,
      category: "other",
      include: false,
    }));
    setItems((list) => [...list, ...added]);
    let next = 0;
    await Promise.all(
      Array.from({ length: Math.min(parallel, sources.length) }, async () => {
        while (next < sources.length) {
          const n = next++;
          const item = added[n];
          const src = sources[n];
          if (!item || !src) continue;
          try {
            const preview = await withReauth(src.source);
            const r = preview.review;
            update(item.key, {
              state: "ready",
              preview,
              category: r?.category || "other",
              include: !r?.duplicate,
            });
          } catch (err) {
            update(item.key, { state: "failed", error: message(err) });
          }
        }
      }),
    );
  }

  async function addFiles(files: FileList | File[]) {
    setError("");
    const list = [...files];
    const tooBig = list.filter((f) => f.size > maxFile);
    if (tooBig.length > 0) {
      setError(`${tooBig.map((f) => f.name).join(", ")}: bigger than 1 MiB, which no egg is.`);
    }
    await review(
      list
        .filter((f) => f.size <= maxFile)
        .map((f) => ({
          label: f.name,
          source: async () => {
            const data = new Uint8Array(await f.arrayBuffer());
            return eggClient.reviewEggFile({ orgId, source: { case: "file", value: data } });
          },
        })),
    );
  }

  async function addLinks() {
    const urls = links
      .split(/\s+/)
      .map((l) => l.trim())
      .filter(Boolean);
    setLinks("");
    await review(
      urls.map((url) => ({
        label: url,
        source: () => eggClient.reviewEggFile({ orgId, source: { case: "url", value: url } }),
      })),
    );
  }

  async function importAll() {
    setBusy(true);
    setError("");
    for (const item of items) {
      if (!item.include || item.state !== "ready" || !item.preview) continue;
      update(item.key, { state: "importing" });
      try {
        const preview = item.preview;
        await withReauth(() =>
          eggClient.importEggFile({
            orgId,
            egg: preview.egg,
            sourceUrl: preview.sourceUrl,
            category: item.category,
          }),
        );
        update(item.key, { state: "imported", include: false });
      } catch (err) {
        update(item.key, { state: "failed", error: message(err), include: false });
      }
    }
    await client.invalidateQueries();
    setBusy(false);
  }

  function drop(e: DragEvent) {
    e.preventDefault();
    setDragging(false);
    if (e.dataTransfer.files.length > 0) void addFiles(e.dataTransfer.files);
  }

  const chosen = items.filter((i) => i.include && i.state === "ready");
  const reviewing = items.some((i) => i.state === "reviewing");
  const imported = items.filter((i) => i.state === "imported").length;
  const warned = chosen.filter((i) => (i.preview?.review?.warnings.length ?? 0) > 0).length;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        onOpenChange(o);
        if (!o) reset();
      }}
    >
      <DialogContent className="sm:max-w-3xl">
        <div className="flex max-h-[80vh] min-w-0 flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Import eggs</DialogTitle>
            <DialogDescription>
              Pterodactyl eggs, as JSON or YAML: as many files and links as you like. Each one is
              reviewed before anything is saved.
            </DialogDescription>
          </DialogHeader>
          <div className="-mx-1 flex min-w-0 flex-col gap-4 overflow-y-auto px-1">
            <label
              htmlFor="egg-files"
              onDragOver={(e) => {
                e.preventDefault();
                setDragging(true);
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={drop}
              className={cn(
                "flex cursor-pointer flex-col items-center gap-1 rounded-lg border border-dashed p-5 text-center text-sm text-muted-foreground transition-colors hover:bg-muted/40",
                dragging && "border-primary bg-muted/60",
              )}
            >
              <FileUp className="size-5" />
              <span>
                <span className="font-medium text-foreground">Choose egg files</span> or drop them
                here
              </span>
              <input
                id="egg-files"
                type="file"
                multiple
                accept=".json,.yaml,.yml,application/json,application/yaml"
                className="sr-only"
                onChange={(e) => {
                  if (e.target.files) void addFiles(e.target.files);
                  e.target.value = "";
                }}
              />
            </label>
            <div className="grid gap-2">
              <Label htmlFor="egg-links">Links</Label>
              <div className="flex gap-2">
                <textarea
                  id="egg-links"
                  rows={2}
                  placeholder="One link per line: https://github.com/…/egg.json"
                  className="min-w-0 flex-1 rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                  value={links}
                  onChange={(e) => setLinks(e.target.value)}
                />
                <Button
                  type="button"
                  variant="outline"
                  disabled={!links.trim()}
                  onClick={() => void addLinks()}
                >
                  Add
                </Button>
              </div>
            </div>
            {items.length > 0 && (
              <ul className="flex flex-col divide-y rounded-lg border">
                {items.map((i) => (
                  <EggRow
                    key={i.key}
                    item={i}
                    disabled={busy}
                    onChange={(next) => update(i.key, next)}
                    onRemove={() => setItems((list) => list.filter((x) => x.key !== i.key))}
                  />
                ))}
              </ul>
            )}
            {chosen.length > 0 && (
              <Alert>
                <ShieldAlert />
                <AlertTitle>Eggs are programs that run on your nodes</AlertTitle>
                <AlertDescription>
                  Each install script runs as root in a container, with the server's files and the
                  internet, and its images run the server. Import only eggs you trust.
                  {warned > 0 &&
                    ` ${warned} of these ${warned === 1 ? "has warnings" : "have warnings"}: open ${warned === 1 ? "it" : "them"} to see why.`}
                </AlertDescription>
              </Alert>
            )}
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          {chosen.length > 0 && (
            <Label className="flex items-start gap-2 text-sm font-normal">
              <Checkbox
                checked={trusted}
                onCheckedChange={(c) => setTrusted(c === true)}
                className="mt-0.5"
              />
              {orgId
                ? "I've looked at what these eggs run, and I trust them on this org's nodes."
                : "I've looked at what these eggs run, and I trust them on every org's nodes."}
            </Label>
          )}
          <DialogFooter className="items-center">
            {imported > 0 && (
              <p className="mr-auto text-sm text-muted-foreground">
                {imported} {imported === 1 ? "egg" : "eggs"} imported.
              </p>
            )}
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => {
                onOpenChange(false);
                reset();
              }}
            >
              {imported > 0 ? "Done" : "Cancel"}
            </Button>
            <Button
              onClick={importAll}
              disabled={busy || reviewing || !trusted || chosen.length === 0}
            >
              {busy && <Loader2 className="animate-spin" />}
              {busy
                ? "Importing…"
                : `Import ${chosen.length > 0 ? chosen.length : ""} ${chosen.length === 1 ? "egg" : "eggs"}`}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function EggRow({
  item: i,
  disabled,
  onChange,
  onRemove,
}: {
  item: Item;
  disabled: boolean;
  onChange: (next: Partial<Item>) => void;
  onRemove: () => void;
}) {
  const r = i.preview?.review;
  const status =
    i.state === "reviewing"
      ? "Reviewing…"
      : i.state === "importing"
        ? "Importing…"
        : i.state === "imported"
          ? "Imported"
          : i.state === "failed"
            ? (i.error ?? "Failed")
            : r?.duplicate
              ? "Already in the library"
              : r?.updates
                ? `New version of ${r.updates}`
                : "";
  return (
    <li className="flex flex-col gap-2 p-3 text-sm">
      <div className="flex min-w-0 items-center gap-3">
        <Checkbox
          aria-label={`Import ${r?.name || i.label}`}
          checked={i.include}
          disabled={disabled || i.state !== "ready"}
          onCheckedChange={(c) => onChange({ include: c === true })}
        />
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{r?.name || i.label}</p>
          <p
            className={cn(
              "truncate text-xs text-muted-foreground",
              i.state === "failed" && "text-destructive",
            )}
          >
            {status ||
              [r?.author, r?.images.map((x) => x.name).join(", ")].filter(Boolean).join(" · ") ||
              i.label}
          </p>
        </div>
        {i.state === "reviewing" || i.state === "importing" ? (
          <Loader2 className="size-4 animate-spin text-muted-foreground" />
        ) : null}
        {r && (r.warnings.length ?? 0) > 0 && (
          <Badge variant="outline" className="gap-1 border-warn/40 text-warn">
            <TriangleAlert className="size-3" /> {r.warnings.length}
          </Badge>
        )}
        {i.state === "ready" && (
          <select
            aria-label="Category"
            className="h-7 rounded-md border border-input bg-transparent px-1.5 text-xs dark:bg-input/30"
            value={i.category}
            disabled={disabled}
            onChange={(e) => onChange({ category: e.target.value })}
          >
            {categories.map((c) => (
              <option key={c} value={c}>
                {categoryName(c)}
              </option>
            ))}
          </select>
        )}
        {r && (
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={i.open ? "Hide what it runs" : "Show what it runs"}
            aria-expanded={!!i.open}
            onClick={() => onChange({ open: !i.open })}
          >
            <ChevronDown className={cn("transition-transform", i.open && "rotate-180")} />
          </Button>
        )}
        {i.state !== "importing" && i.state !== "imported" && (
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={`Remove ${r?.name || i.label}`}
            disabled={disabled}
            onClick={onRemove}
          >
            <X />
          </Button>
        )}
      </div>
      {i.open && r && (
        <div className="flex min-w-0 flex-col gap-2 pl-7">
          {r.warnings.map((w) => (
            <p key={w.kind + w.text} className="flex gap-2 text-xs">
              <TriangleAlert className="size-3.5 shrink-0 text-warn" /> {w.text}
            </p>
          ))}
          <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            Images from
            {r.registries.map((x) => (
              <Badge key={x} variant="outline">
                {x}
              </Badge>
            ))}
          </p>
          <pre className="max-h-48 overflow-auto terminal rounded-lg p-2.5 font-mono text-xs leading-relaxed whitespace-pre">
            {r.installScript || "(no install script)"}
          </pre>
          <p className="text-xs text-muted-foreground">
            Starts with <code className="break-all">{r.startup[0] || "(none)"}</code>
          </p>
        </div>
      )}
    </li>
  );
}
