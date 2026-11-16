import { useQuery as useTanQuery } from "@tanstack/react-query";
import { Download, FileUp, Loader2, MoreHorizontal, Upload } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";

import { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { IdentifiedAddon } from "@/gen/raptor/panel/v1/addon_pb";
import {
  type AddonList,
  changes,
  diffList,
  exportList,
  type Installed,
  type KindList,
  kindOf,
  ListError,
  type LockedAddon,
  listFileName,
  parseList,
  pickVersion,
  type SourceFile,
  stringifyList,
} from "@/lib/addon-list";
import { addonKinds, loaderName, sourceNames } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { addonClient } from "@/lib/transport";

// Add-ons as code (#437, docs/EGGS.md#add-on-lists): the Add-ons tab's
// menu, to export the installed add-ons as a list and to apply one. A
// list names add-ons by source, project, and version; applying it looks
// each one up through the Panel again, and Wings checks every file's hash
// as on any install.

type File = { name: string; sha512?: string };

// installedOf is the add-on folder's files with what each was identified
// as.
function installedOf(files: File[], byHash: Map<string, IdentifiedAddon>): Installed[] {
  return files.map((f) => {
    const a = f.sha512 ? byHash.get(f.sha512) : undefined;
    if (!a?.project || !a.version) return { file: f.name, sha512: f.sha512 };
    const file = a.version.file;
    return {
      file: f.name,
      sha512: f.sha512,
      addon: {
        source: a.project.source || "modrinth",
        project: a.project.id,
        version_id: a.version.id,
        name: a.project.title,
        version_number: a.version.versionNumber,
        sha512: file?.sha512 || undefined,
        sha256: file?.sha256 || undefined,
        sha1: file?.sha1 || undefined,
      },
    };
  });
}

function save(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export function AddonListMenu({
  orgId,
  nodeId,
  serverId,
  serverName,
  kind,
  loaders,
  version,
  dir,
  files,
  addons,
  identified,
  canWrite,
  onApplied,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  serverName: string;
  kind: string;
  loaders: string[];
  version: string;
  dir?: string;
  files: File[];
  addons: IdentifiedAddon[];
  // The files have been looked up (or there are none to look up).
  identified: boolean;
  canWrite: boolean;
  onApplied: (note: string, error: string) => Promise<void>;
}) {
  const [importing, setImporting] = useState(false);
  const installed = useMemo(
    () => installedOf(files, new Map(addons.map((a) => [a.sha512, a]))),
    [files, addons],
  );
  const noun = addonKinds[kind]?.noun ?? "add-on";
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button size="icon-sm" variant="ghost" aria-label={`${noun} list`}>
              <MoreHorizontal />
            </Button>
          }
        />
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuItem
            disabled={!identified}
            onClick={() =>
              save(
                listFileName(serverName, kind),
                stringifyList(exportList(kind, loaders, version, installed)),
              )
            }
          >
            <Download /> Export list
          </DropdownMenuItem>
          {canWrite && (
            <DropdownMenuItem disabled={!identified || !dir} onClick={() => setImporting(true)}>
              <Upload /> Import list
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {importing && dir && (
        <ImportDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          kind={kind}
          loaders={loaders}
          version={version}
          dir={dir}
          installed={installed}
          onClose={() => setImporting(false)}
          onApplied={onApplied}
        />
      )}
    </>
  );
}

const textarea =
  "min-h-32 w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

function ImportDialog({
  onClose,
  ...props
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  loaders: string[];
  version: string;
  dir: string;
  installed: Installed[];
  onClose: () => void;
  onApplied: (note: string, error: string) => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [list, setList] = useState<KindList>();
  const [error, setError] = useState("");
  const noun = addonKinds[props.kind]?.noun ?? "add-on";
  const [busy, setBusy] = useState(false);

  function read(t: string) {
    setError("");
    try {
      const parsed: AddonList = parseList(t);
      setList(kindOf(parsed, props.kind, `${noun}s`));
    } catch (err) {
      setError(err instanceof ListError ? err.message : message(err));
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Import a {noun} list</DialogTitle>
          <DialogDescription>
            {list
              ? "Here's what applying it changes. Nothing changes until you apply it."
              : `A list exported from another server's ${noun}s. You'll see what it changes before anything does.`}
          </DialogDescription>
        </DialogHeader>
        {list ? (
          <Review {...props} want={list} busy={busy} setBusy={setBusy} onClose={onClose} />
        ) : (
          <>
            <label className="flex cursor-pointer items-center gap-2 self-start rounded-lg border px-3 py-2 text-sm hover:bg-muted">
              <FileUp className="size-4" /> Choose a file
              <input
                type="file"
                accept=".json,application/json"
                className="sr-only"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (f) read(await f.text());
                }}
              />
            </label>
            <div className="grid gap-1.5">
              <Label htmlFor="addon-list-text">Or paste it</Label>
              <textarea
                id="addon-list-text"
                className={textarea}
                placeholder='{ "version": 1, "kinds": { … } }'
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button disabled={!text.trim()} onClick={() => read(text)}>
                Next
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

type Planned = {
  want: LockedAddon;
  // The file it replaces, for an update.
  replace?: string;
};

type Resolved = {
  ok: { p: Planned; file: SourceFile; versionId: string; versionNumber: string; newest: boolean }[];
  failed: { p: Planned; problem: string }[];
  // Required dependencies neither in the list nor on the server.
  extra: { title: string; versionNumber: string; file: SourceFile }[];
  incompatible: string[];
};

function Review({
  orgId,
  nodeId,
  serverId,
  kind,
  loaders,
  version,
  dir,
  installed,
  want,
  busy,
  setBusy,
  onClose,
  onApplied,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  loaders: string[];
  version: string;
  dir: string;
  installed: Installed[];
  want: KindList;
  busy: boolean;
  setBusy: (b: boolean) => void;
  onClose: () => void;
  onApplied: (note: string, error: string) => Promise<void>;
}) {
  const api = useFilesApi(nodeId, serverId);
  const diff = useMemo(() => diffList(want, installed), [want, installed]);
  const [removing, setRemoving] = useState(true);
  const [newest, setNewest] = useState(false);
  const planned: Planned[] = [
    ...diff.install.map((want) => ({ want })),
    ...diff.update.map((u) => ({ want: u.want, replace: u.have.file })),
  ];
  // Each add-on's version, looked up for this server again.
  const resolved = useTanQuery({
    queryKey: ["addon-list", orgId, kind, loaders, version, newest, planned],
    enabled: planned.length > 0,
    queryFn: async (): Promise<Resolved> => {
      const out: Resolved = { ok: [], failed: [], extra: [], incompatible: [] };
      await Promise.all(
        planned.map(async (p) => {
          try {
            const res = await addonClient.listAddonVersions({
              orgId,
              projectId: p.want.project,
              loaders,
              gameVersion: version,
              source: p.want.source,
            });
            const v = pickVersion(
              p.want,
              res.versions.map((x) => ({
                id: x.id,
                versionNumber: x.versionNumber,
                file: x.file,
              })),
              newest,
            );
            if (v.ok) out.ok.push({ p, ...v });
            else out.failed.push({ p, problem: v.problem });
          } catch (err) {
            out.failed.push({ p, problem: message(err) });
          }
        }),
      );
      // What they need that neither the list nor the server has.
      const have = new Set([
        ...want.addons.map((a) => a.project),
        ...installed.flatMap((f) => (f.addon ? [f.addon.project] : [])),
      ]);
      const extra = new Map<string, Resolved["extra"][number]>();
      const incompatible = new Set<string>();
      await Promise.all(
        out.ok
          .filter((r) => ["modrinth", "factorio", "curseforge"].includes(r.p.want.source))
          .map(async (r) => {
            try {
              const deps = await addonClient.resolveAddonDependencies({
                orgId,
                // CurseForge names a file by its mod and file IDs.
                versionId:
                  r.p.want.source === "curseforge"
                    ? `${r.p.want.project}@${r.versionId}`
                    : r.versionId,
                loaders,
                gameVersion: version,
                installed: [...have],
                source: r.p.want.source,
              });
              for (const d of deps.required) {
                const id = d.project?.id ?? "";
                const f = d.version?.file;
                if (id && f?.url && !have.has(id)) {
                  extra.set(id, {
                    title: d.project?.title ?? id,
                    versionNumber: d.version?.versionNumber ?? "",
                    file: f,
                  });
                }
              }
              for (const p of deps.incompatible) incompatible.add(p.title);
            } catch {
              // Dependencies are a help: the list itself still applies.
            }
          }),
      );
      out.extra = [...extra.values()];
      out.incompatible = [...incompatible];
      return out;
    },
  });
  const r = resolved.data;
  const waiting = planned.length > 0 && !r;
  const count = (r?.ok.length ?? 0) + (r?.extra.length ?? 0) + (removing ? diff.remove.length : 0);
  const differs =
    (want.loaders.length > 0 && want.loaders.join() !== loaders.join()) ||
    (!!want.game_version && !!version && want.game_version !== version);

  async function apply() {
    if (!r) return;
    setBusy(true);
    const failed: string[] = [];
    let done = 0;
    const install = async (name: string, f: SourceFile, replace?: string) => {
      try {
        await api.run("addons.install", {
          kind,
          url: f.url,
          sha512: f.sha512 || undefined,
          sha256: f.sha256 || undefined,
          sha1: f.sha1 || undefined,
          filename: f.filename,
          replace,
        });
        done++;
      } catch (err) {
        failed.push(`${name}: ${message(err)}`);
      }
    };
    // Dependencies first, then the list.
    for (const e of r.extra) await install(e.title, e.file);
    for (const o of r.ok) await install(o.p.want.name ?? o.p.want.project, o.file, o.p.replace);
    if (removing && diff.remove.length > 0) {
      try {
        await api.run("files.delete", { dir, names: diff.remove.map((f) => f.file) });
        done += diff.remove.length;
      } catch (err) {
        failed.push(`Removing: ${message(err)}`);
      }
    }
    const skipped = [
      ...r.failed.map((x) => `${x.p.want.name ?? x.p.want.project}: ${x.problem}`),
      ...failed,
    ];
    const trash = removing && diff.remove.length > 0 ? " (removed files are in the trash)" : "";
    setBusy(false);
    onClose();
    await onApplied(
      done > 0
        ? `Applied the list: ${done} change${done === 1 ? "" : "s"}${trash}. Restart the server to load them.`
        : "",
      skipped.length > 0 ? `Not applied: ${skipped.join("; ")}` : "",
    );
  }

  const name = (a: { name?: string; project: string }) => a.name ?? a.project;
  const ver = (a: { version_number?: string; version_id: string }) =>
    a.version_number ?? a.version_id;
  return (
    <>
      <div className="flex max-h-[60vh] flex-col gap-4 overflow-y-auto text-sm">
        {differs && (
          <p className="rounded-lg border px-3 py-2">
            The list was made on {want.loaders.map(loaderName).join(", ") || "another server"}
            {want.game_version ? ` ${want.game_version}` : ""}; this server runs{" "}
            {loaders.map(loaderName).join(", ")}
            {version ? ` ${version}` : ""}. Versions are looked up for this server.
          </p>
        )}
        {changes(diff) === 0 && diff.copy.length === 0 && (
          <p className="text-muted-foreground">
            This server already matches the list. Nothing to change.
          </p>
        )}
        <Section title="To install" count={diff.install.length}>
          {diff.install.map((a) => (
            <Item
              key={`${a.source}:${a.project}`}
              name={name(a)}
              detail={`${ver(a)} · ${sourceNames[a.source] ?? a.source}`}
              status={status(r, a)}
            />
          ))}
          {r?.extra.map((e) => (
            <Item key={e.title} name={e.title} detail={`${e.versionNumber} · needed by the list`} />
          ))}
        </Section>
        <Section title="To change version" count={diff.update.length}>
          {diff.update.map((u) => (
            <Item
              key={u.have.file}
              name={name(u.want)}
              detail={`${u.have.addon?.version_number ?? u.have.file} → ${ver(u.want)}`}
              status={status(r, u.want)}
            />
          ))}
        </Section>
        <Section title="To remove" count={diff.remove.length}>
          <li>
            <Label className="flex items-center gap-2 font-normal text-muted-foreground">
              <Switch checked={removing} onCheckedChange={setRemoving} disabled={busy} />
              Remove what isn't in the list (to the trash)
            </Label>
          </li>
          {diff.remove.map((f) => (
            <Item
              key={f.file}
              name={f.addon?.name ?? f.file}
              detail={f.file}
              status={removing ? undefined : "kept"}
            />
          ))}
        </Section>
        <Section title="Copy these yourself" count={diff.copy.length}>
          <li className="text-muted-foreground">
            Added by hand on the other server, so Raptor can't fetch them. Upload them in Files.
          </li>
          {diff.copy.map((f) => (
            <Item key={f.sha512} name={f.file} />
          ))}
        </Section>
        <Section title="Kept (added by hand here)" count={diff.kept.length}>
          {diff.kept.map((f) => (
            <Item key={f.file} name={f.file} />
          ))}
        </Section>
        <Section title="Already there" count={diff.same.length}>
          {diff.same.map((a) => (
            <Item key={`${a.source}:${a.project}`} name={name(a)} detail={ver(a)} />
          ))}
        </Section>
        {resolved.error && <p className="text-destructive">{message(resolved.error)}</p>}
        {r && r.incompatible.length > 0 && (
          <p className="text-destructive">
            Some of these can't run beside {r.incompatible.join(", ")}, which{" "}
            {r.incompatible.length === 1 ? "is" : "are"} installed.
          </p>
        )}
        {r && r.failed.length > 0 && (
          <Label className="flex items-center gap-2 font-normal text-muted-foreground">
            <Switch checked={newest} onCheckedChange={setNewest} disabled={busy} />
            Where a listed version can't be installed here, use the newest for this server
          </Label>
        )}
      </div>
      <DialogFooter>
        <Button variant="outline" disabled={busy} onClick={onClose}>
          Cancel
        </Button>
        <Button disabled={busy || waiting || count === 0} onClick={() => void apply()}>
          {(busy || waiting) && <Loader2 className="animate-spin" />}
          {count === 0 ? "Nothing to apply" : `Apply ${count} change${count === 1 ? "" : "s"}`}
        </Button>
      </DialogFooter>
    </>
  );
}

// status is what the lookup found for a listed add-on: its problem, or
// the version it falls back to.
function status(r: Resolved | undefined, a: LockedAddon): string | undefined {
  if (!r) return "looking up…";
  const same = (b: LockedAddon) => b.source === a.source && b.project === a.project;
  const f = r.failed.find((x) => same(x.p.want));
  if (f) return `can't: ${f.problem}`;
  const o = r.ok.find((x) => same(x.p.want));
  return o?.newest ? `newest instead: ${o.versionNumber}` : undefined;
}

function Section({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: ReactNode;
}) {
  if (count === 0) return null;
  return (
    <div className="flex flex-col gap-1">
      <p className="font-medium">
        {title} <span className="text-muted-foreground">({count})</span>
      </p>
      <ul className="flex flex-col gap-0.5">{children}</ul>
    </div>
  );
}

function Item({ name, detail, status }: { name: string; detail?: string; status?: string }) {
  return (
    <li className="flex flex-wrap items-baseline gap-x-2">
      <span>{name}</span>
      {detail && <span className="font-mono text-xs text-muted-foreground">{detail}</span>}
      {status && (
        <span
          className={
            status.startsWith("can't")
              ? "text-xs text-destructive"
              : "text-xs text-muted-foreground"
          }
        >
          {status}
        </span>
      )}
    </li>
  );
}
