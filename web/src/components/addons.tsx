import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useInfiniteQuery, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpCircle,
  CheckCircle2,
  Download,
  ExternalLink,
  Loader2,
  Package,
  Search,
  Trash2,
} from "lucide-react";
import { useDeferredValue, useMemo, useState } from "react";
import { type AddonListing, addonListKey, useInstalledAddons } from "@/components/addons-data";
import { useConfirm } from "@/components/confirm";
import { useFilesApi } from "@/components/file-manager/data";
import { EmptyState } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
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
import { Switch } from "@/components/ui/switch";
import {
  type AddonProject,
  AddonService,
  type IdentifiedAddon,
  type ResolvedAddon,
} from "@/gen/raptor/panel/v1/addon_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { addonKinds, addonSources, loaderName, sourceNames } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { formatBytes as bytes } from "@/lib/metrics";
import { gameVersion, type ServerConfig } from "@/lib/servers";

// The plugin installer and mod manager (docs/EGGS.md#add-ons): what's in
// the server's add-on folder, named from Modrinth by each file's SHA-512
// (or Hangar by its SHA-256) with updates offered, and a search to add
// more. Wings downloads from their CDNs only and keeps a file only if its
// hash is the one asked for.

type Listing = AddonListing;

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

function compact(n: bigint | number): string {
  return Intl.NumberFormat(undefined, { notation: "compact" }).format(Number(n));
}

function Icon({ url }: { url?: string }) {
  return url ? (
    <img
      src={url}
      alt=""
      className="size-9 shrink-0 rounded-lg border object-cover"
      loading="lazy"
    />
  ) : (
    <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
      <Package className="size-4.5" />
    </span>
  );
}

export function AddonsPage({
  orgId,
  nodeId,
  serverId,
  kind,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  canWrite: boolean;
}) {
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const addon = cfg?.egg?.addons?.[kind];
  const meta = addonKinds[kind];
  if (res.error) return <p className="text-sm text-destructive">{message(res.error)}</p>;
  if (!cfg) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (!addon || !meta) {
    return (
      <EmptyState
        icon={Package}
        title={`This server's egg has no ${meta?.noun ?? "add-on"} manager`}
        description="Turn it on in the egg's Add-ons, then reinstall or remake the server from it."
      />
    );
  }
  const version = gameVersion(cfg, addon.version);
  // Rust's plugins need Oxide or Carbon: on vanilla nothing loads them.
  const framework = addon.dir_variable ? cfg.variables?.[addon.dir_variable] : undefined;
  return (
    <>
      {kind === "umod" && framework === "vanilla" && (
        <p className="mb-4 rounded-lg border px-3 py-2 text-sm">
          This server runs vanilla Rust, which doesn't load plugins. Set{" "}
          <strong>Mod framework</strong> to Oxide or Carbon in Startup and restart it, then install
          plugins here.
        </p>
      )}
      <Addons
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        kind={kind}
        loaders={addon.loaders}
        version={version}
        canWrite={canWrite}
      />
    </>
  );
}

function Addons({
  orgId,
  nodeId,
  serverId,
  kind,
  loaders,
  version,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  loaders: string[];
  version: string;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const meta = addonKinds[kind];
  const key = addonListKey(nodeId, serverId, kind);
  const { list, files, known, curseforge } = useInstalledAddons(
    orgId,
    nodeId,
    serverId,
    kind,
    loaders,
    version,
  );
  const byHash = new Map((known.data?.addons ?? []).map((a) => [a.sha512, a]));
  const installedProjects = new Set((known.data?.addons ?? []).map((a) => a.project?.id));
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [needs, setNeeds] = useState<IdentifiedAddon>();
  const { confirm } = useConfirm();
  const refresh = () => client.invalidateQueries({ queryKey: key });
  type Row = Listing["files"][number];
  // Installed files with a newer version for this server's loaders and
  // game version.
  const outdated = files.flatMap((f) => {
    const a = f.sha512 ? byHash.get(f.sha512) : undefined;
    return a?.latest?.file?.url ? [{ f, a }] : [];
  });

  function update(f: Row, a: IdentifiedAddon) {
    const latest = a.latest?.file;
    if (!latest?.url) return Promise.resolve();
    return api.run("addons.install", {
      kind,
      url: latest.url,
      sha512: latest.sha512 || undefined,
      sha256: latest.sha256 || undefined,
      sha1: latest.sha1 || undefined,
      filename: latest.filename,
      replace: f.name,
    });
  }

  async function updateAll() {
    const ok = await confirm({
      title: `Update ${outdated.length} ${meta?.noun}${outdated.length === 1 ? "" : "s"}?`,
      description: (
        <ul className="flex flex-col gap-1 text-sm">
          {outdated.map(({ f, a }) => (
            <li key={f.name}>
              {a.project?.title ?? f.name}:{" "}
              <span className="font-mono text-xs">
                {a.version?.versionNumber ?? "?"} → {a.latest?.versionNumber}
              </span>
            </li>
          ))}
        </ul>
      ),
      confirm: "Update all",
    });
    if (!ok) return;
    setBusy("update-all");
    setError("");
    setNote("");
    let done = 0;
    const failed: string[] = [];
    for (const { f, a } of outdated) {
      try {
        await update(f, a);
        done++;
      } catch (err) {
        failed.push(`${a.project?.title ?? f.name}: ${message(err)}`);
      }
    }
    await refresh();
    setBusy("");
    if (done > 0) setNote(`Updated ${done}. Restart the server to load them.`);
    if (failed.length > 0) setError(`Not updated: ${failed.join("; ")}`);
  }

  const installedIds = [...installedProjects].filter((id): id is string => !!id);
  const identified = known.data?.addons ?? [];
  // Required dependencies an installed add-on has that aren't installed.
  const missingFor = (a?: IdentifiedAddon) =>
    (a?.version?.dependencies ?? []).filter(
      (d) => d.type === "required" && !installedProjects.has(d.projectId),
    );
  // Installed add-ons that need this one.
  const dependentsOf = (a?: IdentifiedAddon) =>
    a?.project
      ? identified.filter((o) =>
          o.version?.dependencies.some(
            (d) => d.type === "required" && d.projectId === a.project?.id,
          ),
        )
      : [];

  // installAll installs files one after another, dependencies first.
  async function installAll(files: InstallFile[], done: string) {
    await act(
      `install:${files.at(-1)?.filename}`,
      async () => {
        for (const f of files) await api.run("addons.install", { kind, ...f });
      },
      `${done} Restart the server to load ${files.length === 1 ? "it" : "them"}.`,
    );
  }
  // Files nobody knows may be the dependency, added by hand.
  const handAdded = !!known.data && files.some((f) => f.sha512 && !byHash.get(f.sha512));

  async function remove(f: { name: string }, a?: IdentifiedAddon) {
    const dependents = dependentsOf(a);
    if (dependents.length > 0) {
      const ok = await confirm({
        title: `Remove ${a?.project?.title ?? f.name}?`,
        description: `${dependents.map((d) => d.project?.title).join(", ")} ${dependents.length === 1 ? "needs" : "need"} it, and won't load without it.`,
        confirm: "Remove anyway",
        destructive: true,
      });
      if (!ok) return;
    }
    await act(
      `remove:${f.name}`,
      () => api.run("files.delete", { dir: list.data?.dir, names: [f.name] }),
      `Moved ${f.name} to the trash. Restart the server to unload it.`,
    );
  }

  async function act(what: string, fn: () => Promise<unknown>, done: string) {
    setBusy(what);
    setError("");
    setNote("");
    try {
      await fn();
      await refresh();
      setNote(done);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="flex flex-col gap-1.5">
              <CardTitle>Installed {meta?.noun}s</CardTitle>
              <CardDescription>
                In <code>{list.data?.dir ?? "…"}</code>
                {kind === "datapacks" ? "" : `, for ${loaders.map(loaderName).join(", ")}`}
                {version ? ` on ${version}` : ""}. Restart the server to load changes.
              </CardDescription>
            </div>
            {known.data && files.length > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-sm text-muted-foreground">
                  {outdated.length === 0
                    ? "All up to date"
                    : `${outdated.length} update${outdated.length === 1 ? "" : "s"} available`}
                </span>
                {canWrite && outdated.length > 1 && (
                  <Button size="sm" disabled={!!busy} onClick={updateAll}>
                    {busy === "update-all" ? (
                      <Loader2 className="animate-spin" />
                    ) : (
                      <ArrowUpCircle />
                    )}
                    Update all
                  </Button>
                )}
              </div>
            )}
          </div>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
          {list.data && files.length === 0 && (
            <p className="text-sm text-muted-foreground">None yet. Find some below.</p>
          )}
          {files.length > 0 && (
            <ul className="divide-y rounded-lg border">
              {files.map((f) => {
                const a = f.sha512 ? byHash.get(f.sha512) : undefined;
                const latest = a?.latest?.file;
                return (
                  <li key={f.name} className="flex flex-wrap items-center gap-3 px-3 py-2.5">
                    <Icon url={a?.project?.iconUrl} />
                    <div className="min-w-0 flex-1 basis-40">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="min-w-0 truncate text-sm font-medium">
                          {a?.project?.title ?? f.name}
                        </span>
                        {a?.version && (
                          <span className="font-mono text-xs text-muted-foreground">
                            {a.version.versionNumber}
                          </span>
                        )}
                        {f.disabled && <Badge variant="outline">Disabled</Badge>}
                        {a?.latest ? (
                          <Badge>Update available</Badge>
                        ) : (
                          a?.version && (
                            <Badge
                              variant="secondary"
                              title={`No newer version${version ? ` for ${version}` : ""}`}
                            >
                              <CheckCircle2 />
                              Up to date
                            </Badge>
                          )
                        )}
                        {!a && f.sha512 && known.data && (
                          <Badge variant="outline">Added by hand</Badge>
                        )}
                        {missingFor(a).length > 0 && (
                          <Badge variant="destructive">
                            {handAdded ? "May need" : "Needs"}{" "}
                            {missingFor(a).length === 1
                              ? "another add-on"
                              : `${missingFor(a).length} more add-ons`}
                          </Badge>
                        )}
                      </div>
                      <p className="truncate text-xs text-muted-foreground">
                        {f.name} · {bytes(f.size)}
                      </p>
                    </div>
                    {canWrite && a?.version && missingFor(a).length > 0 && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!!busy}
                        onClick={() => setNeeds(a)}
                      >
                        <Download />
                        Install what it needs
                      </Button>
                    )}
                    {canWrite && latest?.manualUrl && a?.latest && (
                      <a
                        href={latest.manualUrl}
                        target="_blank"
                        rel="noreferrer"
                        title="Its author only allows downloads from CurseForge: download it, then upload it in Files"
                        className={buttonVariants({ size: "sm", variant: "outline" })}
                      >
                        <ExternalLink />
                        Get {a.latest.versionNumber} on CurseForge
                      </a>
                    )}
                    {canWrite && latest?.url && a?.latest && (
                      <Button
                        size="sm"
                        disabled={!!busy}
                        onClick={() =>
                          act(
                            `update:${f.name}`,
                            () => update(f, a),
                            `Updated to ${a.latest?.versionNumber}. Restart the server to load it.`,
                          )
                        }
                      >
                        {busy === `update:${f.name}` ? (
                          <Loader2 className="animate-spin" />
                        ) : (
                          <ArrowUpCircle />
                        )}
                        Update to {a.latest.versionNumber}
                      </Button>
                    )}
                    {canWrite && (
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Remove ${f.name}`}
                        className="text-muted-foreground hover:text-destructive"
                        disabled={!!busy}
                        onClick={() => void remove(f, a)}
                      >
                        <Trash2 />
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {note && <p className="text-sm text-muted-foreground">{note}</p>}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
      </Card>
      {canWrite && (
        <Finder
          orgId={orgId}
          kind={kind}
          loaders={loaders}
          version={version}
          installed={installedProjects}
          installedIds={installedIds}
          curseforge={curseforge}
          onInstall={installAll}
        />
      )}
      {needs?.version && (
        <NeedsDialog
          orgId={orgId}
          addon={needs}
          loaders={loaders}
          version={version}
          installed={installedIds}
          onClose={() => setNeeds(undefined)}
          onInstall={installAll}
        />
      )}
    </div>
  );
}

type InstallFile = {
  url: string;
  sha512?: string;
  sha256?: string;
  sha1?: string;
  filename: string;
};

function Finder({
  orgId,
  kind,
  loaders,
  version,
  installed,
  installedIds,
  curseforge,
  onInstall,
}: {
  orgId: string;
  kind: string;
  loaders: string[];
  version: string;
  installed: Set<string | undefined>;
  installedIds: string[];
  curseforge: boolean;
  onInstall: (files: InstallFile[], done: string) => Promise<void>;
}) {
  const meta = addonKinds[kind];
  const [query, setQuery] = useState("");
  // Mods that only run on players' computers (shaders, minimaps) are left
  // out: on a server they crash it or do nothing.
  const [clientOnly, setClientOnly] = useState(false);
  const [picked, setPicked] = useState<AddonProject>();
  const sources = addonSources(kind, loaders, curseforge);
  const [source, setSource] = useState(sources[0] ?? "modrinth");
  const q = useDeferredValue(query.trim());
  const search = useInfiniteQuery(
    AddonService.method.searchAddons,
    {
      orgId,
      kind,
      loaders,
      gameVersion: version,
      query: q,
      offset: 0,
      source,
      includeClientOnly: clientOnly,
    },
    {
      pageParamKey: "offset",
      getNextPageParam: (last, pages) => {
        const seen = pages.reduce((n, p) => n + p.projects.length, 0);
        return seen < last.total && last.projects.length > 0 ? seen : undefined;
      },
    },
  );
  const projects = search.data?.pages.flatMap((p) => p.projects) ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Find {meta?.noun}s</CardTitle>
        <CardDescription>
          From {sourceNames[source]}
          {version ? `, for ${version}` : ", for any game version"}. Most downloaded first.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {sources.length > 1 && (
          <div className="flex gap-1.5">
            {sources.map((s) => (
              <Button
                key={s}
                size="sm"
                variant={source === s ? "secondary" : "ghost"}
                aria-pressed={source === s}
                onClick={() => setSource(s)}
              >
                {sourceNames[s]}
              </Button>
            ))}
          </div>
        )}
        {kind === "mods" && source === "modrinth" && (
          <Label className="flex items-center gap-2 self-start font-normal text-muted-foreground">
            <Switch checked={clientOnly} onCheckedChange={setClientOnly} />
            Show client-only mods (they don't run on a server)
          </Label>
        )}
        <div className="relative sm:max-w-sm">
          <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            type="search"
            aria-label={`Search ${meta?.noun}s`}
            placeholder={`Search ${meta?.noun}s…`}
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        {search.error && <p className="text-sm text-destructive">{message(search.error)}</p>}
        {search.data && projects.length === 0 && (
          <p className="text-sm text-muted-foreground">Nothing matches.</p>
        )}
        <ul className="grid grid-cols-1 gap-2 md:grid-cols-2">
          {projects.map((p) => (
            <li
              key={p.id}
              className="flex min-w-0 flex-wrap items-start gap-3 rounded-lg border p-3"
            >
              <Icon url={p.iconUrl} />
              <div className="min-w-0 flex-1 basis-48">
                <div className="flex items-center gap-1.5">
                  <a
                    href={p.url}
                    target="_blank"
                    rel="noreferrer"
                    className="truncate text-sm font-medium hover:underline"
                  >
                    {p.title}
                  </a>
                  <ExternalLink className="size-3 shrink-0 text-muted-foreground" />
                  {p.serverSide === "unsupported" && <Badge variant="outline">Client-only</Badge>}
                </div>
                <p className="text-xs text-muted-foreground">
                  by {p.author} · {compact(p.downloads)} downloads
                </p>
                <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{p.description}</p>
              </div>
              {installed.has(p.id) ? (
                <Badge variant="secondary">Installed</Badge>
              ) : (
                <Button size="sm" variant="outline" onClick={() => setPicked(p)}>
                  <Download /> Install
                </Button>
              )}
            </li>
          ))}
        </ul>
        {search.hasNextPage && (
          <Button
            variant="outline"
            className="self-center"
            disabled={search.isFetchingNextPage}
            onClick={() => search.fetchNextPage()}
          >
            {search.isFetchingNextPage && <Loader2 className="animate-spin" />} Show more
          </Button>
        )}
      </CardContent>
      {picked && (
        <InstallDialog
          orgId={orgId}
          project={picked}
          loaders={loaders}
          version={version}
          installed={installedIds}
          onClose={() => setPicked(undefined)}
          onInstall={onInstall}
        />
      )}
    </Card>
  );
}

function InstallDialog({
  orgId,
  project,
  loaders,
  version,
  installed,
  onClose,
  onInstall,
}: {
  orgId: string;
  project: AddonProject;
  loaders: string[];
  version: string;
  installed: string[];
  onClose: () => void;
  onInstall: (files: InstallFile[], done: string) => Promise<void>;
}) {
  const versions = useQuery(AddonService.method.listAddonVersions, {
    orgId,
    projectId: project.id,
    loaders,
    gameVersion: version,
    source: project.source,
  });
  const from = sourceNames[project.source] ?? "Modrinth";
  const [chosen, setChosen] = useState("");
  const [busy, setBusy] = useState(false);
  const list = versions.data?.versions ?? [];
  const v = list.find((x) => x.id === chosen) ?? list[0];
  // What else it needs (Modrinth, Factorio, and CurseForge list
  // dependencies; Hangar's aren't read).
  const resolves = ["modrinth", "factorio", "curseforge"].includes(project.source);
  const deps = useQuery(
    AddonService.method.resolveAddonDependencies,
    {
      orgId,
      versionId: project.source === "curseforge" && v?.id ? `${project.id}@${v.id}` : (v?.id ?? ""),
      loaders,
      gameVersion: version,
      installed,
      source: project.source,
    },
    { enabled: resolves && !!v?.id && !v?.file?.manualUrl },
  );
  const [optional, setOptional] = useState<Set<string>>(new Set());
  const [alone, setAlone] = useState(false);
  const required = alone ? [] : (deps.data?.required ?? []);
  const blocked = (deps.data?.incompatible.length ?? 0) > 0;
  const waiting = resolves && !!v?.id && !v?.file?.manualUrl && deps.isPending;
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Install {project.title}</DialogTitle>
          <DialogDescription>
            Wings downloads it from {from} and checks it's exactly the file {from} lists.
          </DialogDescription>
        </DialogHeader>
        {versions.error ? (
          <p className="text-sm text-destructive">{message(versions.error)}</p>
        ) : !versions.data ? (
          <p className="text-sm text-muted-foreground">Finding versions…</p>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No version runs on {loaders.map(loaderName).join(", ")}
            {version ? ` ${version}` : ""}.
          </p>
        ) : (
          <div className="grid gap-1.5">
            <Label htmlFor="addon-version">Version</Label>
            <select
              id="addon-version"
              className={select}
              value={v?.id}
              onChange={(e) => setChosen(e.target.value)}
            >
              {list.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.versionNumber} · {x.gameVersions.slice(-3).join(", ")}
                  {x.publishedAt ? ` · ${dateOnly(timestampDate(x.publishedAt))}` : ""}
                </option>
              ))}
            </select>
            {v?.file && (
              <p className="text-xs text-muted-foreground">
                {v.file.filename} · {bytes(Number(v.file.size))}
              </p>
            )}
            {v?.file?.manualUrl && (
              <p className="text-sm">
                Its author only allows downloads from CurseForge itself, so Raptor can't install it.
                Download it from CurseForge, then upload it in Files.
              </p>
            )}
          </div>
        )}
        {deps.error && <p className="text-sm text-destructive">{message(deps.error)}</p>}
        {deps.data && (
          <Dependencies
            data={deps.data}
            alone={alone}
            optional={optional}
            onOptional={setOptional}
            onAlone={setAlone}
          />
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          {v?.file?.manualUrl ? (
            <a
              href={v.file.manualUrl}
              target="_blank"
              rel="noreferrer"
              className={buttonVariants()}
            >
              <ExternalLink /> Download on CurseForge
            </a>
          ) : (
            <Button
              disabled={busy || !v?.file || waiting || (blocked && !alone)}
              onClick={async () => {
                if (!v?.file) return;
                setBusy(true);
                const extra = alone
                  ? []
                  : (deps.data?.optional ?? []).filter((o) => optional.has(o.project?.id ?? ""));
                const more = filesOf([...required, ...extra]);
                await onInstall(
                  [...more, fileOf(v.file)],
                  more.length === 0
                    ? `Installed ${v.file.filename}.`
                    : `Installed ${project.title} and ${more.length} more it uses.`,
                );
                setBusy(false);
                onClose();
              }}
            >
              {(busy || waiting) && <Loader2 className="animate-spin" />}
              {required.length > 0 ? `Install with ${required.length} more` : "Install"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function fileOf(f: {
  url: string;
  sha512: string;
  sha256: string;
  sha1: string;
  filename: string;
}): InstallFile {
  return {
    url: f.url,
    sha512: f.sha512 || undefined,
    sha256: f.sha256 || undefined,
    sha1: f.sha1 || undefined,
    filename: f.filename,
  };
}

function filesOf(list: ResolvedAddon[]): InstallFile[] {
  return list.flatMap((r) => (r.version?.file ? [fileOf(r.version.file)] : []));
}

type Resolution = {
  required: ResolvedAddon[];
  optional: ResolvedAddon[];
  incompatible: AddonProject[];
  unavailable: AddonProject[];
};

// Dependencies says what installing an add-on brings with it (#434):
// what it needs (installed with it), what it can use (ticked to add), and
// what stops it.
function Dependencies({
  data,
  alone,
  optional,
  onOptional,
  onAlone,
}: {
  data: Resolution;
  alone?: boolean;
  optional?: Set<string>;
  onOptional?: (s: Set<string>) => void;
  onAlone?: (on: boolean) => void;
}) {
  const nothing =
    data.required.length +
      data.optional.length +
      data.incompatible.length +
      data.unavailable.length ===
    0;
  if (nothing) return null;
  return (
    <div className="flex flex-col gap-3 text-sm">
      {data.incompatible.length > 0 && (
        <p className="text-destructive">
          It can't run beside {data.incompatible.map((p) => p.title).join(", ")}, which{" "}
          {data.incompatible.length === 1 ? "is" : "are"} installed. Remove{" "}
          {data.incompatible.length === 1 ? "it" : "them"} first.
        </p>
      )}
      {!alone && data.required.length > 0 && (
        <div className="flex flex-col gap-1">
          <p className="font-medium">Also installs what it needs</p>
          <ul className="flex flex-col gap-0.5 text-muted-foreground">
            {data.required.map((r) => (
              <li key={r.project?.id}>
                {r.project?.title}{" "}
                <span className="font-mono text-xs">{r.version?.versionNumber}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {!alone && data.unavailable.length > 0 && (
        <p className="text-muted-foreground">
          It also needs {data.unavailable.map((p) => p.title).join(", ")}, which can't be installed
          from here for this version: add {data.unavailable.length === 1 ? "it" : "them"} by hand,
          or it may not load.
        </p>
      )}
      {!alone && optional && onOptional && data.optional.length > 0 && (
        <div className="flex flex-col gap-1">
          <p className="font-medium">Works with (optional)</p>
          {data.optional.map((r) => {
            const id = r.project?.id ?? "";
            return (
              <label key={id} className="flex items-center gap-2 text-muted-foreground">
                <Checkbox
                  checked={optional.has(id)}
                  onCheckedChange={(on) => {
                    const next = new Set(optional);
                    if (on) next.add(id);
                    else next.delete(id);
                    onOptional(next);
                  }}
                />
                {r.project?.title}{" "}
                <span className="font-mono text-xs">{r.version?.versionNumber}</span>
              </label>
            );
          })}
        </div>
      )}
      {onAlone && (data.required.length > 0 || data.incompatible.length > 0) && (
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <Checkbox checked={alone} onCheckedChange={(on) => onAlone(!!on)} />
          Install only this, without what it needs (you'll add it yourself)
        </label>
      )}
    </div>
  );
}

// NeedsDialog installs what an installed add-on needs and doesn't have.
function NeedsDialog({
  orgId,
  addon,
  loaders,
  version,
  installed,
  onClose,
  onInstall,
}: {
  orgId: string;
  addon: IdentifiedAddon;
  loaders: string[];
  version: string;
  installed: string[];
  onClose: () => void;
  onInstall: (files: InstallFile[], done: string) => Promise<void>;
}) {
  const deps = useQuery(AddonService.method.resolveAddonDependencies, {
    orgId,
    versionId:
      addon.project?.source === "curseforge"
        ? `${addon.project.id}@${addon.version?.id ?? ""}`
        : (addon.version?.id ?? ""),
    loaders,
    gameVersion: version,
    installed,
    source: ["factorio", "curseforge"].includes(addon.project?.source ?? "")
      ? (addon.project?.source ?? "")
      : "",
  });
  const [busy, setBusy] = useState(false);
  const required = deps.data?.required ?? [];
  const title = addon.project?.title ?? "This add-on";
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>What {title} needs</DialogTitle>
          <DialogDescription>
            It won't load until these are installed. Each is checked like any other install.
          </DialogDescription>
        </DialogHeader>
        {deps.error ? (
          <p className="text-sm text-destructive">{message(deps.error)}</p>
        ) : !deps.data ? (
          <p className="text-sm text-muted-foreground">Looking up what it needs…</p>
        ) : required.length === 0 && deps.data.unavailable.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Everything it needs is installed (perhaps added by hand).
          </p>
        ) : (
          <Dependencies data={{ ...deps.data, optional: [] }} />
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || required.length === 0}
            onClick={async () => {
              setBusy(true);
              const files = filesOf(required);
              await onInstall(
                files,
                `Installed what ${title} needs: ${required.map((r) => r.project?.title).join(", ")}.`,
              );
              setBusy(false);
              onClose();
            }}
          >
            {busy && <Loader2 className="animate-spin" />} Install {required.length || ""}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
