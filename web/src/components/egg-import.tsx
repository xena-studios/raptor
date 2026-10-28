import { useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import { FileUp, Link2, ShieldAlert, TriangleAlert } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ReviewEggFileResponse } from "@/gen/raptor/panel/v1/egg_pb";
import { categories, categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { eggClient } from "@/lib/transport";

const maxFile = 1 << 20;

// The egg a re-import replaces: its file becomes a new version.
export type ReplacedEgg = {
  id: string;
  name: string;
  version: number;
  summary: string;
  category: string;
  sourceUrl: string;
  sha256: string;
};

// EggImport imports an egg from a link or a file (docs/PANEL.md#imported-eggs):
// first what it would run, then, once the admin says they trust it, the
// import. An egg's install script runs as root in a container on the node,
// so the review isn't skippable. orgId empty imports an official egg. With
// replace, the file is a new version of that egg instead (a re-import).
export function EggImport({
  orgId,
  open,
  onOpenChange,
  onImported,
  replace,
}: {
  orgId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onImported?: (id: string) => void;
  replace?: ReplacedEgg;
}) {
  const client = useQueryClient();
  const withReauth = useReauth();
  const [category, setCategory] = useState("other");
  const [from, setFrom] = useState<"url" | "file">("url");
  const [url, setUrl] = useState(replace?.sourceUrl ?? "");
  const [keepRaptor, setKeepRaptor] = useState(true);
  const [file, setFile] = useState<File>();
  const [preview, setPreview] = useState<ReviewEggFileResponse>();
  const [trusted, setTrusted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function reset() {
    setPreview(undefined);
    setTrusted(false);
    setError("");
  }

  async function review(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (from === "file") {
        if (!file) throw new Error("Choose an egg file.");
        if (file.size > maxFile)
          throw new Error("That file is bigger than 1 MiB, which no egg is.");
        const data = new Uint8Array(await file.arrayBuffer());
        setPreview(
          await withReauth(() =>
            eggClient.reviewEggFile({ orgId, source: { case: "file", value: data } }),
          ),
        );
      } else {
        setPreview(
          await withReauth(() =>
            eggClient.reviewEggFile({ orgId, source: { case: "url", value: url } }),
          ),
        );
      }
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function importIt() {
    if (!preview) return;
    setError("");
    setBusy(true);
    try {
      let id: string;
      if (replace) {
        await withReauth(() =>
          eggClient.updateEgg({
            orgId,
            id: replace.id,
            change: { case: "file", value: preview.egg },
            summary: replace.summary,
            category: replace.category,
            note: "Re-imported",
            baseVersion: replace.version,
            keepRaptor,
            sourceUrl: preview.sourceUrl,
          }),
        );
        id = replace.id;
      } else {
        id = (
          await withReauth(() =>
            eggClient.importEggFile({
              orgId,
              egg: preview.egg,
              sourceUrl: preview.sourceUrl,
              category,
            }),
          )
        ).id;
      }
      await client.invalidateQueries();
      onImported?.(id);
      onOpenChange(false);
      reset();
      setUrl(replace?.sourceUrl ?? "");
      setFile(undefined);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const r = preview?.review;
  // A re-import may match another egg in the library; only the egg's own
  // file is nothing new.
  const same = replace ? preview?.sha256 === replace.sha256 : !!r?.duplicate;
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) reset();
      }}
    >
      <DialogContent className={cn(r ? "sm:max-w-3xl" : "sm:max-w-lg")}>
        {!r ? (
          <form onSubmit={review} className="flex min-w-0 flex-col gap-4">
            <DialogHeader>
              <DialogTitle>
                {replace ? `Import a new version of ${replace.name}` : "Import an egg"}
              </DialogTitle>
              <DialogDescription>
                {replace
                  ? "Its file is replaced with this one, as a new version. Servers keep the version they run until you update them. "
                  : "A Pterodactyl or Pelican egg, as JSON or YAML. "}
                You'll see what it runs before anything is saved.
              </DialogDescription>
            </DialogHeader>
            <fieldset className="flex self-start rounded-lg border p-0.5">
              <legend className="sr-only">From</legend>
              {(
                [
                  ["url", "From a link", Link2],
                  ["file", "From a file", FileUp],
                ] as const
              ).map(([value, label, Icon]) => (
                <button
                  key={value}
                  type="button"
                  aria-pressed={from === value}
                  onClick={() => {
                    setFrom(value);
                    setError("");
                  }}
                  className={cn(
                    "flex items-center gap-1.5 rounded-md px-2.5 py-1 text-sm text-muted-foreground transition-colors hover:text-foreground",
                    from === value && "bg-muted font-medium text-foreground",
                  )}
                >
                  <Icon className="size-3.5" /> {label}
                </button>
              ))}
            </fieldset>
            {from === "url" ? (
              <div className="grid gap-2">
                <Label htmlFor="egg-url">Link</Label>
                <Input
                  id="egg-url"
                  type="url"
                  required
                  autoFocus
                  placeholder="https://github.com/pelican-eggs/games-standalone/blob/main/…/egg.json"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  A link to the file itself. GitHub file pages work too.
                </p>
              </div>
            ) : (
              <div className="grid gap-2">
                <Label htmlFor="egg-file">Egg file</Label>
                <Input
                  id="egg-file"
                  type="file"
                  required
                  accept=".json,.yaml,.yml,application/json,application/yaml"
                  onChange={(e) => setFile(e.target.files?.[0])}
                />
              </div>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? "Reading…" : "Review"}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="flex max-h-[80vh] min-w-0 flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Review "{r.name || "Unnamed egg"}"</DialogTitle>
              <DialogDescription>
                {r.author ? `By ${r.author}. ` : ""}
                {preview.sourceUrl ? (
                  <>
                    From{" "}
                    <a
                      href={preview.sourceUrl}
                      target="_blank"
                      rel="noreferrer"
                      className="break-all underline underline-offset-2"
                    >
                      {preview.sourceUrl}
                    </a>
                    .
                  </>
                ) : (
                  "From a file you uploaded."
                )}
              </DialogDescription>
            </DialogHeader>
            <div className="-mx-1 flex min-w-0 flex-col gap-4 overflow-y-auto px-1">
              <Alert>
                <ShieldAlert />
                <AlertTitle>An egg is a program that runs on your nodes</AlertTitle>
                <AlertDescription>
                  Its install script runs as root in a container, with the server's files and the
                  internet, and its images run the server. Import it only if you trust where it came
                  from.
                </AlertDescription>
              </Alert>
              {r.warnings.length > 0 && (
                <ul className="flex flex-col gap-2">
                  {r.warnings.map((w) => (
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
              {r.description && (
                <p className="line-clamp-3 text-sm text-muted-foreground">{r.description}</p>
              )}
              <Section
                title="Images"
                note="The server runs in one of these; you pick which when you create it."
              >
                <ul className="flex flex-col gap-1">
                  {r.images.map((i) => (
                    <li key={i.ref} className="flex flex-wrap items-baseline gap-2 text-sm">
                      <span className="text-muted-foreground">{i.name}</span>
                      <code className="break-all">{i.ref}</code>
                    </li>
                  ))}
                </ul>
                <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                  Registries:
                  {r.registries.map((x) => (
                    <Badge key={x} variant="outline">
                      {x}
                    </Badge>
                  ))}
                </p>
              </Section>
              <Section
                title="Install script"
                note={`Runs once, in ${r.installContainer || "the default container"}${r.installEntrypoint ? ` with ${r.installEntrypoint}` : ""}.`}
              >
                <pre className="max-h-72 overflow-auto terminal rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre">
                  {r.installScript || "(none)"}
                </pre>
              </Section>
              <Section title="Startup command">
                <pre className="overflow-x-auto terminal rounded-lg p-3 font-mono text-xs whitespace-pre-wrap break-all">
                  {r.startup[0] || "(none)"}
                </pre>
              </Section>
              <p className="text-xs text-muted-foreground">
                {r.variables.length} {r.variables.length === 1 ? "setting" : "settings"} ·{" "}
                {r.arch.length ? `Runs on ${r.arch.join(" and ")}` : "CPUs not stated"} ·{" "}
                {r.format === "pelican" ? "Pelican" : "Pterodactyl"} format · SHA-256{" "}
                <code>{preview.sha256.slice(0, 12)}</code>
              </p>
            </div>
            {!same && replace && (
              <Label className="flex items-start gap-2 text-sm font-normal">
                <Checkbox
                  checked={keepRaptor}
                  onCheckedChange={(c) => setKeepRaptor(c === true)}
                  className="mt-0.5"
                />
                <span>
                  Keep the egg's Raptor settings (CPUs, players, add-ons) if this file has none
                  <span className="block text-xs text-muted-foreground">
                    Files from Pterodactyl and Pelican don't have them.
                  </span>
                </span>
              </Label>
            )}
            {!same && !replace && (
              <div className="grid gap-1.5">
                <Label htmlFor="egg-category">Category</Label>
                <select
                  id="egg-category"
                  className="h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                  value={category}
                  onChange={(e) => setCategory(e.target.value)}
                >
                  {categories.map((c) => (
                    <option key={c} value={c}>
                      {categoryName(c)}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {same ? (
              <p className="text-sm text-muted-foreground">
                {replace
                  ? "This is the file the egg has now."
                  : "This egg is already in the library."}
              </p>
            ) : (
              <Label className="flex items-start gap-2 text-sm font-normal">
                <Checkbox
                  checked={trusted}
                  onCheckedChange={(c) => setTrusted(c === true)}
                  className="mt-0.5"
                />
                {orgId
                  ? "I've looked at what this egg runs, and I trust it on this org's nodes."
                  : "I've looked at what this egg runs, and I trust it on every org's nodes."}
              </Label>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={reset}>
                Back
              </Button>
              <Button onClick={importIt} disabled={busy || !trusted || same}>
                {busy ? "Importing…" : replace ? "Save as a new version" : "Import"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Section({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-sm font-medium">{title}</h3>
      {note && <p className="text-xs text-muted-foreground">{note}</p>}
      {children}
    </section>
  );
}
