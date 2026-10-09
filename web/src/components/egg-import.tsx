import { useQueryClient } from "@tanstack/react-query";
import { FileUp, Link2, ShieldAlert, TriangleAlert } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

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
import type { PreviewEggResponse } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

const maxFile = 1 << 20;

// EggImport imports an egg from a link or a file (docs/PANEL.md#imported-eggs):
// first what it would run, then, once the admin says they trust it, the
// import. An egg's install script runs as root in a container on the node,
// so the review isn't skippable.
export function EggImport({
  orgId,
  open,
  onOpenChange,
}: {
  orgId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const client = useQueryClient();
  const [from, setFrom] = useState<"url" | "file">("url");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File>();
  const [preview, setPreview] = useState<PreviewEggResponse>();
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
        setPreview(await orgClient.previewEgg({ orgId, source: { case: "file", value: data } }));
      } else {
        setPreview(await orgClient.previewEgg({ orgId, source: { case: "url", value: url } }));
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
      await orgClient.importEgg({ orgId, egg: preview.egg, sourceUrl: preview.sourceUrl });
      await client.invalidateQueries();
      onOpenChange(false);
      reset();
      setUrl("");
      setFile(undefined);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const r = preview?.review;
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
              <DialogTitle>Import an egg</DialogTitle>
              <DialogDescription>
                A Pterodactyl or Pelican egg, as JSON or YAML. You'll see what it runs before
                anything is saved.
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
                      className="flex gap-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-2.5 text-sm"
                    >
                      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-500" />
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
                <pre className="max-h-72 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed whitespace-pre">
                  {r.installScript || "(none)"}
                </pre>
              </Section>
              <Section title="Startup command">
                <pre className="overflow-x-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap break-all">
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
            {r.duplicate ? (
              <p className="text-sm text-muted-foreground">This egg is already imported.</p>
            ) : (
              <Label className="flex items-start gap-2 text-sm font-normal">
                <Checkbox
                  checked={trusted}
                  onCheckedChange={(c) => setTrusted(c === true)}
                  className="mt-0.5"
                />
                I've looked at what this egg runs, and I trust it on this org's nodes.
              </Label>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={reset}>
                Back
              </Button>
              <Button onClick={importIt} disabled={busy || !trusted || r.duplicate}>
                {busy ? "Importing…" : "Import"}
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
