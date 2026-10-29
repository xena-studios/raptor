import { create } from "@bufbuild/protobuf";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import {
  Boxes,
  Circle,
  CircleCheck,
  FileCheck,
  Loader2,
  Map as MapIcon,
  Pencil,
  Plug,
  Plus,
  Star,
  Trash2,
  Users,
  X,
} from "lucide-react";
import { type ComponentType, lazy, type ReactNode, Suspense, useState } from "react";

import { tabClass } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { EggImageSchema, type EggVariable } from "@/gen/raptor/panel/v1/catalog_pb";
import {
  type EggAddon,
  EggAddonSchema,
  type EggConfigFile,
  EggConfigFileSchema,
  EggFindSchema,
  type EggSpec,
  EggStartupSchema,
} from "@/gen/raptor/panel/v1/egg_pb";
import {
  access,
  accessNames,
  addonKinds,
  addons,
  blockers,
  categories,
  categoryName,
  type EditorState,
  knownFeatures,
  loaderName,
  parsers,
  signals,
} from "@/lib/eggs";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";
import { eggClient } from "@/lib/transport";

import type { Library } from "./library";
import { field, select, VariableDialog } from "./variable-dialog";

const MonacoEditor = lazy(() => import("@/components/monaco"));

const tabs = [
  ["overview", "Overview"],
  ["runtimes", "Runtimes"],
  ["variables", "Variables"],
  ["startup", "Startup"],
  ["install", "Install"],
  ["config", "Config files"],
  ["addons", "Add-ons"],
] as const;
type Tab = (typeof tabs)[number][0] | "file";

type Patch = (next: Partial<EggSpec>) => void;

// EggEditor makes or edits an egg. Saving writes the fields into the egg's
// file (a new version); the File tab edits the file itself.
export function EggEditor({
  lib,
  initial,
  eggId,
  version,
  file,
}: {
  lib: Library;
  initial: EditorState;
  // Set when editing.
  eggId?: string;
  version?: number;
  file?: Uint8Array;
}) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const withReauth = useReauth();
  const [state, setState] = useState(initial);
  const [tab, setTab] = useState<Tab>("overview");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [ver, setVer] = useState(version ?? 0);
  const spec = state.spec;
  const patch: Patch = (next) => {
    setSaved(false);
    setState((s) => ({ ...s, spec: { ...s.spec, ...next } }));
  };
  const problems = blockers(spec);

  async function save() {
    if (problems.length > 0) {
      setError(problems[0] ?? "");
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (!eggId) {
        const res = await withReauth(() =>
          eggClient.createEgg({
            orgId: lib.orgId,
            spec,
            summary: state.summary,
            category: state.category,
          }),
        );
        await client.invalidateQueries();
        navigate(lib.detail(res.id));
        return;
      }
      const res = await withReauth(() =>
        eggClient.updateEgg({
          orgId: lib.orgId,
          id: eggId,
          change: { case: "spec", value: spec },
          summary: state.summary,
          category: state.category,
          note,
          baseVersion: ver,
        }),
      );
      setVer(res.version);
      await client.invalidateQueries();
      setNote("");
      setSaved(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <nav aria-label="Egg sections" className="-mb-2 flex gap-4 overflow-x-auto border-b">
        {tabs.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={tabClass}
            data-status={tab === key ? "active" : undefined}
            aria-current={tab === key ? "page" : undefined}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
        {eggId && file && (
          <button
            type="button"
            className={tabClass}
            data-status={tab === "file" ? "active" : undefined}
            onClick={() => setTab("file")}
          >
            File
          </button>
        )}
      </nav>
      {tab === "overview" && (
        <OverviewTab
          state={state}
          setState={(s) => {
            setSaved(false);
            setState(s);
          }}
        />
      )}
      {tab === "runtimes" && <RuntimesTab spec={spec} patch={patch} />}
      {tab === "variables" && <VariablesTab spec={spec} patch={patch} />}
      {tab === "startup" && <StartupTab spec={spec} patch={patch} />}
      {tab === "install" && <InstallTab spec={spec} patch={patch} />}
      {tab === "config" && <ConfigTab spec={spec} patch={patch} />}
      {tab === "addons" && <AddonsTab spec={spec} patch={patch} />}
      {tab === "file" && eggId && file && (
        <FileTab lib={lib} eggId={eggId} version={ver} state={state} file={file} />
      )}
      {tab !== "file" && (
        <div className="sticky bottom-0 -mx-6 flex flex-wrap items-center justify-end gap-2 border-t bg-background/95 px-6 py-3 backdrop-blur">
          {error ? (
            <p className="mr-auto text-sm text-destructive">{error}</p>
          ) : saved ? (
            <p className="mr-auto text-sm text-muted-foreground">Saved as a new version.</p>
          ) : (
            problems.length > 0 && (
              <p className="mr-auto text-sm text-muted-foreground">{problems[0]}</p>
            )
          )}
          {eggId && (
            <Input
              aria-label="What changed"
              placeholder="What changed (optional)"
              className="w-56"
              maxLength={200}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          )}
          <Button variant="ghost" onClick={() => navigate(eggId ? lib.detail(eggId) : lib.list)}>
            {eggId ? "Back" : "Cancel"}
          </Button>
          <Button disabled={busy} onClick={save}>
            {busy && <Loader2 className="animate-spin" />}
            {eggId ? "Save" : "Create egg"}
          </Button>
        </div>
      )}
    </div>
  );
}

function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
      {children}
    </div>
  );
}

function OverviewTab({
  state,
  setState,
}: {
  state: EditorState;
  setState: (s: EditorState) => void;
}) {
  const spec = state.spec;
  const set = (next: Partial<EggSpec>) => setState({ ...state, spec: { ...spec, ...next } });
  return (
    <div className="grid max-w-2xl gap-4">
      <Field id="egg-name" label="Name">
        <Input
          id="egg-name"
          maxLength={100}
          placeholder="Paper with Geyser"
          value={spec.name}
          onChange={(e) => set({ name: e.target.value })}
        />
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="egg-category" label="Category">
          <select
            id="egg-category"
            className={select}
            value={state.category}
            onChange={(e) => setState({ ...state, category: e.target.value })}
          >
            {categories.map((c) => (
              <option key={c} value={c}>
                {categoryName(c)}
              </option>
            ))}
          </select>
        </Field>
        <Field id="egg-author" label="Author">
          <Input
            id="egg-author"
            placeholder="you@example.com"
            value={spec.author}
            onChange={(e) => set({ author: e.target.value })}
          />
        </Field>
      </div>
      <Field id="egg-summary" label="Summary" hint="One line, for the egg's card.">
        <Input
          id="egg-summary"
          maxLength={160}
          value={state.summary}
          onChange={(e) => setState({ ...state, summary: e.target.value })}
        />
      </Field>
      <fieldset className="grid gap-1.5">
        <legend className="mb-1.5 text-sm font-medium">CPUs</legend>
        <div className="flex gap-4">
          {[
            ["amd64", "x86 (Intel, AMD)"],
            ["arm64", "ARM"],
          ].map(([a, label]) => (
            <Label key={a} className="flex items-center gap-2 text-sm font-normal">
              <Checkbox
                checked={spec.arch.includes(a ?? "")}
                onCheckedChange={(on) =>
                  set({ arch: on ? [...spec.arch, a ?? ""] : spec.arch.filter((x) => x !== a) })
                }
              />
              {label}
            </Label>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          Which machines it runs on; nodes with other CPUs don't offer it. Neither checked: not
          stated, and every node offers it.
        </p>
      </fieldset>
      <Field id="egg-description" label="Description">
        <textarea
          id="egg-description"
          rows={5}
          maxLength={2000}
          className={field}
          placeholder="What it runs, and anything people should know."
          value={spec.description}
          onChange={(e) => set({ description: e.target.value })}
        />
      </Field>
    </div>
  );
}

function RuntimesTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const images = spec.images;
  const update = (i: number, next: { name?: string; ref?: string }) =>
    patch({ images: images.map((im, n) => (n === i ? { ...im, ...next } : im)) });
  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        The images a server can run in, like a Java version. The first is the default; people pick
        another in the server's settings.
      </p>
      {images.length === 0 && <Empty>No runtimes yet.</Empty>}
      {images.map((im, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no IDs; their order is their identity
        <div key={i} className="flex flex-wrap items-end gap-2 rounded-lg border p-3">
          <div className="grid min-w-36 flex-1 gap-1.5">
            <Label htmlFor={`rt-name-${i}`} className="text-xs">
              Name
            </Label>
            <Input
              id={`rt-name-${i}`}
              placeholder="Java 21"
              value={im.name}
              onChange={(e) => update(i, { name: e.target.value })}
            />
          </div>
          <div className="grid min-w-56 flex-[2] gap-1.5">
            <Label htmlFor={`rt-ref-${i}`} className="text-xs">
              Image
            </Label>
            <Input
              id={`rt-ref-${i}`}
              className="font-mono text-xs"
              placeholder="ghcr.io/pterodactyl/yolks:java_21"
              value={im.ref}
              onChange={(e) => update(i, { ref: e.target.value.trim() })}
            />
          </div>
          <Button
            type="button"
            size="sm"
            variant={i === 0 ? "secondary" : "outline"}
            disabled={i === 0}
            onClick={() => patch({ images: [im, ...images.filter((_, n) => n !== i)] })}
          >
            <Star /> {i === 0 ? "Default" : "Make default"}
          </Button>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label="Remove runtime"
            onClick={() => patch({ images: images.filter((_, n) => n !== i) })}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="self-start"
        onClick={() => patch({ images: [...images, create(EggImageSchema, {})] })}
      >
        <Plus /> Add runtime
      </Button>
    </div>
  );
}

function VariablesTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const [editing, setEditing] = useState<number | "new">();
  const vars = spec.variables;
  function save(v: EggVariable) {
    patch({
      variables: editing === "new" ? [...vars, v] : vars.map((o, i) => (i === editing ? v : o)),
    });
    setEditing(undefined);
  }
  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          Settings a server is set up with. Use them in the startup command as {"{{KEY}}"}.
        </p>
        <Button type="button" variant="outline" size="sm" onClick={() => setEditing("new")}>
          <Plus /> Add variable
        </Button>
      </div>
      {vars.length === 0 ? (
        <Empty>No variables yet.</Empty>
      ) : (
        <ul className="divide-y rounded-lg border">
          {vars.map((v, i) => (
            <li key={v.env || i} className="flex items-center gap-3 px-4 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium">{v.name}</span>
                  <Badge variant="outline">{accessNames[access(v)].label}</Badge>
                  {v.rules.includes("required") && <Badge variant="secondary">Required</Badge>}
                </div>
                <p className="mt-0.5 font-mono text-xs text-muted-foreground">
                  {v.env}
                  {v.default ? ` = ${v.default}` : ""}
                </p>
              </div>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label={`Edit ${v.name}`}
                onClick={() => setEditing(i)}
              >
                <Pencil />
              </Button>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label={`Remove ${v.name}`}
                onClick={() => patch({ variables: vars.filter((_, n) => n !== i) })}
              >
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {editing !== undefined && (
        <VariableDialog
          initial={editing === "new" ? undefined : vars[editing]}
          taken={vars.filter((_, i) => i !== editing).map((v) => v.env)}
          onSave={save}
          onClose={() => setEditing(undefined)}
        />
      )}
    </div>
  );
}

function StartupTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const startup = spec.startup.length
    ? spec.startup
    : [create(EggStartupSchema, { name: "Default", command: "" })];
  const several = startup.length > 1;
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      {startup.map((c, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no IDs; their order is their identity
        <div key={i} className="grid gap-1.5">
          <div className="flex items-center justify-between gap-2">
            <Label htmlFor={`start-${i}`}>
              {several ? `Startup command: ${c.name}` : "Startup command"}
            </Label>
            {several && i > 0 && (
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Remove startup command"
                onClick={() => patch({ startup: startup.filter((_, n) => n !== i) })}
              >
                <X />
              </Button>
            )}
          </div>
          <textarea
            id={`start-${i}`}
            rows={3}
            className={cn(field, "font-mono text-xs")}
            placeholder="java -Xms128M -XX:MaxRAMPercentage=95.0 -jar {{SERVER_JARFILE}}"
            value={c.command}
            onChange={(e) =>
              patch({
                startup: startup.map((o, n) => (n === i ? { ...o, command: e.target.value } : o)),
              })
            }
          />
          {i === 0 && (
            <p className="text-xs text-muted-foreground">
              {"{{KEY}}"} is replaced by a variable; {"{{SERVER_MEMORY}}"} and {"{{SERVER_PORT}}"}{" "}
              by the server's memory and port.
            </p>
          )}
        </div>
      ))}
      <div className="grid gap-4 sm:grid-cols-[12rem_1fr]">
        <Field id="stop-type" label="Stop with">
          <select
            id="stop-type"
            className={select}
            value={spec.stopType}
            onChange={(e) =>
              patch({
                stopType: e.target.value,
                stopValue:
                  e.target.value === "signal"
                    ? "SIGINT"
                    : e.target.value === "command"
                      ? "stop"
                      : "",
              })
            }
          >
            <option value="command">A console command</option>
            <option value="signal">A signal</option>
            <option value="native">The image's default</option>
          </select>
        </Field>
        {spec.stopType === "command" && (
          <Field id="stop-value" label="Command">
            <Input
              id="stop-value"
              className="font-mono"
              value={spec.stopValue}
              onChange={(e) => patch({ stopValue: e.target.value })}
            />
          </Field>
        )}
        {spec.stopType === "signal" && (
          <Field id="stop-value" label="Signal">
            <select
              id="stop-value"
              className={select}
              value={spec.stopValue}
              onChange={(e) => patch({ stopValue: e.target.value })}
            >
              {signals.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </Field>
        )}
      </div>
      <div className="grid gap-2">
        <Label>Ready when the console shows</Label>
        <p className="text-xs text-muted-foreground">
          Until then the server shows as starting. Leave empty to count it as up once it starts.
          Spaces count.
        </p>
        {spec.done.map((line, i) => {
          const regex = line.startsWith("regex:");
          const text = regex ? line.slice(6) : line;
          const set = (r: boolean, t: string) =>
            patch({ done: spec.done.map((o, n) => (n === i ? (r ? `regex:${t}` : t) : o)) });
          return (
            // biome-ignore lint/suspicious/noArrayIndexKey: rows have no IDs; their order is their identity
            <div key={i} className="flex items-center gap-2">
              <select
                aria-label={`Ready line ${i + 1} kind`}
                className={cn(select, "w-36")}
                value={regex ? "regex" : "text"}
                onChange={(e) => set(e.target.value === "regex", text)}
              >
                <option value="text">Contains</option>
                <option value="regex">Matches regex</option>
              </select>
              <Input
                aria-label={`Ready line ${i + 1}`}
                className="flex-1 font-mono text-xs"
                placeholder={regex ? "^Done \\(" : ")! For help, type "}
                value={text}
                onChange={(e) => set(regex, e.target.value)}
              />
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Remove ready line"
                onClick={() => patch({ done: spec.done.filter((_, n) => n !== i) })}
              >
                <Trash2 />
              </Button>
            </div>
          );
        })}
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="self-start"
          onClick={() => patch({ done: [...spec.done, ""] })}
        >
          <Plus /> Add a line
        </Button>
      </div>
    </div>
  );
}

function InstallTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const settings = useFileSettings();
  // Monaco is told the script once per mount; typing goes to it and back.
  const [start] = useState(spec.installScript);
  return (
    <div className="flex max-w-4xl flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Runs once, as root in its own container, when a server is made or reinstalled. The server's
        files are in <code>/mnt/server</code>. Leave it empty if the image needs no setup.
      </p>
      <div className="grid gap-4 sm:grid-cols-[1fr_10rem]">
        <Field id="install-image" label="Install image">
          <Input
            id="install-image"
            className="font-mono text-xs"
            placeholder="ghcr.io/pterodactyl/installers:debian"
            value={spec.installContainer}
            onChange={(e) => patch({ installContainer: e.target.value.trim() })}
          />
        </Field>
        <Field id="install-shell" label="Shell">
          <select
            id="install-shell"
            className={select}
            value={spec.installEntrypoint}
            onChange={(e) => patch({ installEntrypoint: e.target.value })}
          >
            {["bash", "ash", "sh"].map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <div className="grid gap-1.5">
        <Label>Install script</Label>
        <Card className="h-[28rem] overflow-hidden py-0">
          <Suspense
            fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
          >
            <MonacoEditor
              value={start}
              language="shell"
              readOnly={false}
              settings={settings}
              onChange={(text) => patch({ installScript: text })}
              onSave={() => {}}
            />
          </Suspense>
        </Card>
      </div>
    </div>
  );
}

function ConfigTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const files = spec.files;
  const update = (i: number, f: EggConfigFile) =>
    patch({ files: files.map((o, n) => (n === i ? f : o)) });
  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          Values Wings sets in the server's files before every start, like the port in
          server.properties. Values can use {"{{server.build.default.port}}"} and{" "}
          {"{{server.build.env.KEY}}"}.
        </p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() =>
            patch({ files: [...files, create(EggConfigFileSchema, { parser: "properties" })] })
          }
        >
          <Plus /> Add file
        </Button>
      </div>
      {files.length === 0 && <Empty>No config files.</Empty>}
      {files.map((f, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no IDs; their order is their identity
        <Card key={i}>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-wrap items-end gap-2">
              <div className="grid min-w-56 flex-1 gap-1.5">
                <Label htmlFor={`cfg-path-${i}`}>File</Label>
                <Input
                  id={`cfg-path-${i}`}
                  className="font-mono text-xs"
                  placeholder="server.properties"
                  value={f.path}
                  onChange={(e) => update(i, { ...f, path: e.target.value.trim() })}
                />
              </div>
              <div className="grid w-52 gap-1.5">
                <Label htmlFor={`cfg-parser-${i}`}>Format</Label>
                <select
                  id={`cfg-parser-${i}`}
                  className={select}
                  value={f.parser === "yml" ? "yaml" : f.parser}
                  onChange={(e) => update(i, { ...f, parser: e.target.value })}
                >
                  {parsers.map(([p, label]) => (
                    <option key={p} value={p}>
                      {label}
                    </option>
                  ))}
                </select>
              </div>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Remove file"
                onClick={() => patch({ files: files.filter((_, n) => n !== i) })}
              >
                <Trash2 />
              </Button>
            </div>
            <div className="flex flex-col gap-2">
              {f.find.map((r, j) => {
                const set = (next: Partial<typeof r>) =>
                  update(i, {
                    ...f,
                    find: f.find.map((o, n) => (n === j ? { ...o, ...next } : o)),
                  });
                return (
                  // biome-ignore lint/suspicious/noArrayIndexKey: rows have no IDs; their order is their identity
                  <div key={j} className="flex flex-wrap items-center gap-2">
                    <Input
                      aria-label="Key"
                      className="min-w-40 flex-1 font-mono text-xs"
                      placeholder={f.parser === "file" ? "line starting with" : "server-port"}
                      value={r.key}
                      onChange={(e) => set({ key: e.target.value })}
                    />
                    {r.ifValue !== "" && (
                      <Input
                        aria-label="Only when it's"
                        className="w-32 font-mono text-xs"
                        value={r.ifValue}
                        onChange={(e) => set({ ifValue: e.target.value })}
                      />
                    )}
                    <span className="text-xs text-muted-foreground">=</span>
                    <Input
                      aria-label="Value"
                      className="min-w-40 flex-[2] font-mono text-xs"
                      placeholder="{{server.build.default.port}}"
                      value={r.value}
                      onChange={(e) => set({ value: e.target.value })}
                    />
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Remove"
                      onClick={() => update(i, { ...f, find: f.find.filter((_, n) => n !== j) })}
                    >
                      <X />
                    </Button>
                  </div>
                );
              })}
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="self-start"
                onClick={() => update(i, { ...f, find: [...f.find, create(EggFindSchema, {})] })}
              >
                <Plus /> Add a value
              </Button>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function AddonCard({
  icon: Icon,
  title,
  description,
  on,
  onToggle,
  children,
}: {
  icon: ComponentType<{ className?: string; strokeWidth?: number }>;
  title: string;
  description: string;
  on: boolean;
  onToggle: () => void;
  children?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 rounded-xl bg-card p-4 transition-colors",
        on ? "ring-2 ring-primary" : "ring-1 ring-foreground/10",
      )}
    >
      <button
        type="button"
        aria-pressed={on}
        onClick={onToggle}
        className="flex items-start gap-3 text-left"
      >
        <span
          className={cn(
            "flex size-9 shrink-0 items-center justify-center rounded-lg",
            on ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="size-4.5" strokeWidth={1.75} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-medium">{title}</span>
          <span className="mt-0.5 block text-xs text-muted-foreground">{description}</span>
        </span>
        {on ? (
          <CircleCheck className="size-5 shrink-0 text-primary" />
        ) : (
          <Circle className="size-5 shrink-0 text-muted-foreground/30" />
        )}
      </button>
      {on && children}
    </div>
  );
}

function AddonsTab({ spec, patch }: { spec: EggSpec; patch: Patch }) {
  const has = (f: string) => spec.features.includes(f);
  const toggle = (f: string) =>
    patch({ features: has(f) ? spec.features.filter((x) => x !== f) : [...spec.features, f] });
  const other = spec.features.filter((f) => !knownFeatures.includes(f));
  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Extra tools servers made from this egg get. Turn on the ones that fit what it runs.
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <AddonCard
          icon={FileCheck}
          title={addons.eula.label}
          description={addons.eula.description}
          on={has("eula")}
          onToggle={() => toggle("eula")}
        />
        <AddonCard
          icon={Users}
          title={addons.players.label}
          description={addons.players.description}
          on={!!spec.playersQuery}
          onToggle={() =>
            patch({ playersQuery: spec.playersQuery ? "" : "minecraft", playersPort: "" })
          }
        >
          <div className="grid grid-cols-2 gap-2">
            <select
              aria-label="Query protocol"
              className={select}
              value={spec.playersQuery}
              onChange={(e) => patch({ playersQuery: e.target.value })}
            >
              <option value="minecraft">Minecraft (Java)</option>
              <option value="source">Source (Steam games)</option>
            </select>
            <Input
              aria-label="Query port"
              placeholder="Server's port"
              value={spec.playersPort}
              onChange={(e) => patch({ playersPort: e.target.value.trim() })}
            />
          </div>
          <p className="text-xs text-muted-foreground">
            The port: empty for the server's own, +1 for the next one, or a variable's key.
          </p>
        </AddonCard>
        {Object.entries(addonKinds).map(([kind, meta]) => {
          const a = spec.addons.find((x) => x.kind === kind);
          const set = (next: Partial<EggAddon>) =>
            patch({ addons: spec.addons.map((x) => (x.kind === kind ? { ...x, ...next } : x)) });
          return (
            <AddonCard
              key={kind}
              icon={kind === "plugins" ? Plug : kind === "datapacks" ? MapIcon : Boxes}
              title={meta.label}
              description={meta.description}
              on={!!a}
              onToggle={() =>
                patch({
                  addons: a
                    ? spec.addons.filter((x) => x.kind !== kind)
                    : [
                        ...spec.addons,
                        create(EggAddonSchema, {
                          kind,
                          loaders: meta.dflt,
                          dir: meta.dir,
                          version:
                            spec.variables.find((v) => /^(MINECRAFT|MC)_VERSION$/.test(v.env))
                              ?.env ?? "",
                        }),
                      ],
                })
              }
            >
              {a && (
                <div className="flex flex-col gap-3">
                  <fieldset className="flex flex-wrap gap-x-4 gap-y-1.5">
                    <legend className="mb-1 text-xs text-muted-foreground">Runs on</legend>
                    {meta.loaders.map((l) => (
                      <Label key={l} className="flex items-center gap-1.5 text-sm font-normal">
                        <Checkbox
                          checked={a.loaders.includes(l)}
                          onCheckedChange={(on) =>
                            set({
                              loaders: on ? [...a.loaders, l] : a.loaders.filter((x) => x !== l),
                            })
                          }
                        />
                        {loaderName(l)}
                      </Label>
                    ))}
                  </fieldset>
                  <div className="grid grid-cols-2 gap-2">
                    <div className="grid gap-1">
                      <Label
                        htmlFor={`addon-dir-${kind}`}
                        className="text-xs text-muted-foreground"
                      >
                        Folder
                      </Label>
                      <Input
                        id={`addon-dir-${kind}`}
                        className="font-mono text-xs"
                        value={a.dir}
                        onChange={(e) => set({ dir: e.target.value.trim() })}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label
                        htmlFor={`addon-version-${kind}`}
                        className="text-xs text-muted-foreground"
                      >
                        Game version from
                      </Label>
                      <select
                        id={`addon-version-${kind}`}
                        className={select}
                        value={a.version}
                        onChange={(e) => set({ version: e.target.value })}
                      >
                        <option value="">Any version</option>
                        {spec.variables.map((v) => (
                          <option key={v.env} value={v.env}>
                            {v.name}
                          </option>
                        ))}
                      </select>
                    </div>
                  </div>
                </div>
              )}
            </AddonCard>
          );
        })}
      </div>
      {other.length > 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-muted-foreground">
            Other features the egg lists, for panels that use them. They're kept as they are.
          </p>
          <div className="flex flex-wrap gap-2">
            {other.map((f) => (
              <span
                key={f}
                className="inline-flex items-center gap-1.5 rounded-md border py-1 pr-1 pl-2 font-mono text-xs text-muted-foreground"
              >
                {f}
                <button
                  type="button"
                  aria-label={`Remove ${f}`}
                  className="rounded-sm p-0.5 hover:bg-accent hover:text-foreground"
                  onClick={() => toggle(f)}
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// FileTab edits the egg file itself, for what the form doesn't cover.
function FileTab({
  lib,
  eggId,
  version,
  state,
  file,
}: {
  lib: Library;
  eggId: string;
  version: number;
  state: EditorState;
  file: Uint8Array;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const withReauth = useReauth();
  const settings = useFileSettings();
  const [start] = useState(() => new TextDecoder().decode(file));
  const [text, setText] = useState(start);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const json = start.trimStart().startsWith("{");
  async function save() {
    setBusy(true);
    setError("");
    try {
      await withReauth(() =>
        eggClient.updateEgg({
          orgId: lib.orgId,
          id: eggId,
          change: { case: "file", value: new TextEncoder().encode(text) },
          summary: state.summary,
          category: state.category,
          note: "Edited the file",
          baseVersion: version,
        }),
      );
      await client.invalidateQueries();
      navigate(lib.detail(eggId));
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex flex-col gap-3">
      <Alert>
        <AlertDescription>
          The egg file, as Pterodactyl and Pelican read it. Saving it replaces any unsaved changes
          in the other tabs.
        </AlertDescription>
      </Alert>
      <Card className="h-[36rem] overflow-hidden py-0">
        <Suspense
          fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
        >
          <MonacoEditor
            value={start}
            language={json ? "json" : "yaml"}
            readOnly={false}
            settings={settings}
            onChange={setText}
            onSave={save}
          />
        </Suspense>
      </Card>
      <div className="flex items-center justify-end gap-2">
        {error && <p className="mr-auto text-sm text-destructive">{error}</p>}
        <Button disabled={busy || text === start} onClick={save}>
          {busy && <Loader2 className="animate-spin" />} Save file
        </Button>
      </div>
    </div>
  );
}
