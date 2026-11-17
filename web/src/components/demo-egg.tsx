import { ShieldAlert, TriangleAlert, Upload } from "lucide-react";
import { type ReactNode, useRef, useState } from "react";

import { EggEditor } from "@/components/eggs/editor";
import type { Library } from "@/components/eggs/library";
import { PageHeader } from "@/components/page";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { EggReview } from "@/gen/raptor/panel/v1/egg_pb";
import { blankState, categories, type EditorState } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { demoClient } from "@/lib/transport";

// The largest egg file the Panel reads (eggimport.MaxSize).
const maxEggBytes = 1 << 20;

const cantRun =
  "Eggs you import can't be run in the demo: an anonymous visitor's install script would be arbitrary code on our machine. In your own Raptor, save it and make servers from it.";

// DemoEggPage is New egg in the public demo (#449): import your own egg
// file, see what it would run, and edit and validate it in the editor.
// Nothing is saved, and it can't be run here.
export function DemoEggPage({ lib }: { lib: Library }) {
  const [state, setState] = useState<EditorState | null>(null);
  const [review, setReview] = useState<EggReview | undefined>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  async function load(f: File | undefined) {
    if (!f) return;
    setError("");
    if (f.size > maxEggBytes) {
      setError("That file is over 1 MiB; eggs are smaller.");
      return;
    }
    setBusy(true);
    try {
      const res = await demoClient.previewEgg({
        orgId: lib.orgId,
        file: new Uint8Array(await f.arrayBuffer()),
      });
      if (!res.spec) throw new Error("That file didn't read as an egg.");
      setReview(res.review);
      const category = res.review?.category ?? "";
      setState({
        spec: res.spec,
        summary: "",
        category: (categories as readonly string[]).includes(category) ? category : "other",
      });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        back={{ label: "Eggs", ...lib.list }}
        title="Try your own egg"
        description="Import an egg, see what it runs, and edit it. Nothing is saved in the demo."
      />
      <Alert>
        <ShieldAlert />
        <AlertTitle>Look, don't run</AlertTitle>
        <AlertDescription>{cantRun}</AlertDescription>
      </Alert>
      {!state ? (
        <Card>
          <CardHeader>
            <CardTitle>Egg file</CardTitle>
            <CardDescription>
              A Pterodactyl, Pelican, or Raptor egg (JSON or YAML), up to 1 MiB. Or start from a
              blank one.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-2">
            <input
              ref={input}
              type="file"
              accept=".json,.yaml,.yml,application/json"
              className="sr-only"
              aria-label="Egg file"
              onChange={(e) => load(e.target.files?.[0])}
            />
            <Button disabled={busy} onClick={() => input.current?.click()}>
              <Upload /> {busy ? "Reading…" : "Choose a file"}
            </Button>
            <Button variant="outline" onClick={() => setState(blankState())}>
              Start blank
            </Button>
            {error && <p className="w-full text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      ) : (
        <>
          {review && <ReviewCard r={review} />}
          <EggEditor lib={lib} initial={state} preview />
        </>
      )}
    </>
  );
}

// ReviewCard is what an imported egg would run.
function ReviewCard({ r }: { r: EggReview }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>What "{r.name || "this egg"}" runs</CardTitle>
        <CardDescription>
          {r.author ? `By ${r.author}.` : "From a file you uploaded."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-4 text-sm">
        {r.warnings.length > 0 && (
          <ul className="flex flex-col gap-2">
            {r.warnings.map((w) => (
              <li
                key={w.kind + w.text}
                className="flex gap-2 rounded-lg border border-warn/30 bg-warn-wash p-2.5"
              >
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
                <span>{w.text}</span>
              </li>
            ))}
          </ul>
        )}
        <Part title="Images">
          <ul className="flex flex-col gap-1">
            {r.images.map((i) => (
              <li key={i.ref} className="flex flex-wrap items-baseline gap-2">
                <span className="text-muted-foreground">{i.name}</span>
                <code className="break-all">{i.ref}</code>
              </li>
            ))}
          </ul>
        </Part>
        <Part
          title={`Install script, in ${r.installContainer || "the default container"}${r.installEntrypoint ? ` with ${r.installEntrypoint}` : ""}`}
        >
          <pre className="max-h-60 overflow-auto terminal rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre">
            {r.installScript || "(none)"}
          </pre>
        </Part>
        <Part title="Startup command">
          <pre className="overflow-x-auto terminal rounded-lg p-3 font-mono text-xs whitespace-pre-wrap break-all">
            {r.startup[0] || "(none)"}
          </pre>
        </Part>
      </CardContent>
    </Card>
  );
}

function Part({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      {children}
    </section>
  );
}
