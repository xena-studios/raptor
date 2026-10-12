import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useInfiniteQuery, useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import {
  ArrowUpCircle,
  Download,
  ExternalLink,
  Loader2,
  Package,
  Search,
  Trash2,
} from "lucide-react";
import { useDeferredValue, useMemo, useState } from "react";

import { useFilesApi } from "@/components/file-manager/data";
import { EmptyState } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { type AddonProject, AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { addonKinds, loaderName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { formatBytes as bytes } from "@/lib/metrics";
import { gameVersion, type ServerConfig } from "@/lib/servers";

// The plugin installer and mod manager (docs/EGGS.md#add-ons): what's in
// the server's add-on folder, named from Modrinth by each file's SHA-512
// with updates offered, and a search to add more. Wings downloads from
// Modrinth's CDN only and keeps a file only if its hash is the one asked
// for.

type Listing = {
  kind: string;
  dir: string;
  files: { name: string; size: number; modified_at: number; sha512?: string; disabled?: boolean }[];
};

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
  return (
    <Addons
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      kind={kind}
      loaders={addon.loaders}
      version={version}
      canWrite={canWrite}
    />
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
  const key = ["addons", nodeId, serverId, kind];
  const list = useTanQuery({
    queryKey: key,
    queryFn: () => api.run<Listing>("addons.list", { kind }),
  });
  const files = list.data?.files ?? [];
  const hashes = files.flatMap((f) => (f.sha512 ? [f.sha512] : []));
  const known = useQuery(
    AddonService.method.identifyAddons,
    { orgId, hashes, loaders, gameVersion: version },
    { enabled: hashes.length > 0 },
  );
  const byHash = new Map((known.data?.addons ?? []).map((a) => [a.sha512, a]));
  const installedProjects = new Set((known.data?.addons ?? []).map((a) => a.project?.id));
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const refresh = () => client.invalidateQueries({ queryKey: key });

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
          <CardTitle>Installed {meta?.noun}s</CardTitle>
          <CardDescription>
            In <code>{list.data?.dir ?? "…"}</code>, for {loaders.map(loaderName).join(", ")}
            {version ? ` on ${version}` : ""}. Restart the server to load changes.
          </CardDescription>
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
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-sm font-medium">
                          {a?.project?.title ?? f.name}
                        </span>
                        {a?.version && (
                          <span className="font-mono text-xs text-muted-foreground">
                            {a.version.versionNumber}
                          </span>
                        )}
                        {f.disabled && <Badge variant="outline">Disabled</Badge>}
                        {!a && f.sha512 && known.data && (
                          <Badge variant="outline">Not from Modrinth</Badge>
                        )}
                      </div>
                      <p className="truncate text-xs text-muted-foreground">
                        {f.name} · {bytes(f.size)}
                      </p>
                    </div>
                    {canWrite && latest && a?.latest && (
                      <Button
                        size="sm"
                        disabled={!!busy}
                        onClick={() =>
                          act(
                            `update:${f.name}`,
                            () =>
                              api.run("addons.install", {
                                kind,
                                url: latest.url,
                                sha512: latest.sha512,
                                filename: latest.filename,
                                replace: f.name,
                              }),
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
                        onClick={() =>
                          act(
                            `remove:${f.name}`,
                            () => api.run("files.delete", { dir: list.data?.dir, names: [f.name] }),
                            `Moved ${f.name} to the trash. Restart the server to unload it.`,
                          )
                        }
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
          onInstall={(p) =>
            act(
              `install:${p.filename}`,
              () => api.run("addons.install", { kind, ...p }),
              `Installed ${p.filename}. Restart the server to load it.`,
            )
          }
        />
      )}
    </div>
  );
}

type InstallFile = { url: string; sha512: string; filename: string };

function Finder({
  orgId,
  kind,
  loaders,
  version,
  installed,
  onInstall,
}: {
  orgId: string;
  kind: string;
  loaders: string[];
  version: string;
  installed: Set<string | undefined>;
  onInstall: (f: InstallFile) => Promise<void>;
}) {
  const meta = addonKinds[kind];
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<AddonProject>();
  const q = useDeferredValue(query.trim());
  const search = useInfiniteQuery(
    AddonService.method.searchAddons,
    { orgId, kind, loaders, gameVersion: version, query: q, offset: 0 },
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
          From Modrinth{version ? `, for ${version}` : ", for any game version"}. Most downloaded
          first.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
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
        <ul className="grid gap-2 md:grid-cols-2">
          {projects.map((p) => (
            <li key={p.id} className="flex items-start gap-3 rounded-lg border p-3">
              <Icon url={p.iconUrl} />
              <div className="min-w-0 flex-1">
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
  onClose,
  onInstall,
}: {
  orgId: string;
  project: AddonProject;
  loaders: string[];
  version: string;
  onClose: () => void;
  onInstall: (f: InstallFile) => Promise<void>;
}) {
  const versions = useQuery(AddonService.method.listAddonVersions, {
    orgId,
    projectId: project.id,
    loaders,
    gameVersion: version,
  });
  const [chosen, setChosen] = useState("");
  const [busy, setBusy] = useState(false);
  const list = versions.data?.versions ?? [];
  const v = list.find((x) => x.id === chosen) ?? list[0];
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Install {project.title}</DialogTitle>
          <DialogDescription>
            Wings downloads it from Modrinth and checks it's exactly the file Modrinth lists.
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
                  {x.publishedAt ? ` · ${timestampDate(x.publishedAt).toLocaleDateString()}` : ""}
                </option>
              ))}
            </select>
            {v?.file && (
              <p className="text-xs text-muted-foreground">
                {v.file.filename} · {bytes(Number(v.file.size))}
              </p>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={busy || !v?.file}
            onClick={async () => {
              if (!v?.file) return;
              setBusy(true);
              await onInstall({
                url: v.file.url,
                sha512: v.file.sha512,
                filename: v.file.filename,
              });
              setBusy(false);
              onClose();
            }}
          >
            {busy && <Loader2 className="animate-spin" />} Install
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
