import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink, FileJson, Loader2, Package, Pin, PinOff, Plus, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { type Content, useFilesApi } from "@/components/file-manager/data";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { AddonService, type ReforgerMod } from "@/gen/raptor/panel/v1/addon_pb";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { dateOnly } from "@/lib/format";
import { formatBytes as bytes } from "@/lib/metrics";
import {
  addMods,
  type ConfigMod,
  modLink,
  modRef,
  parseConfig,
  pinMod,
  removeMod,
  sameMods,
  withMods,
} from "@/lib/reforger-mods";
import { base64 } from "@/lib/servers";
import { addonClient } from "@/lib/transport";

const apply = "The server downloads them from the Workshop the next time it starts.";

// ReforgerMods edits an Arma Reforger server's mods (#417,
// docs/EGGS.md#arma-reforger-mods): the game.mods list of its JSON config,
// which the server downloads itself. Names, versions, and what each mod
// needs come from the Workshop through the Panel; without it, mods can
// still be added by ID with a name typed in.
export function ReforgerMods({
  orgId,
  nodeId,
  serverId,
  config,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  config: string;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const readText = async () => {
    try {
      const f = await api.run<Content>("files.read", { path: config });
      return new TextDecoder().decode(fromBase64(f?.data ?? ""));
    } catch {
      return undefined;
    }
  };
  const state = useTanQuery({
    queryKey: ["reforger-mods", nodeId, serverId, config],
    queryFn: async () => {
      const text = await readText();
      return { text, parsed: text === undefined ? undefined : parseConfig(text) };
    },
  });
  if (state.isPending) return <p className="text-sm text-muted-foreground">Reading mods…</p>;
  if (state.error) return <p className="text-sm text-destructive">{message(state.error)}</p>;
  const { text, parsed } = state.data;
  const fileLink = (
    <Link
      to="/orgs/$orgId/servers/$serverId/files"
      params={{ orgId, serverId }}
      search={{ file: config }}
      className="inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline"
    >
      <FileJson className="size-3.5" /> Edit {config} as text
    </Link>
  );
  if (text === undefined || !parsed) {
    return (
      <p className="text-sm text-muted-foreground">
        There's no {config} yet: it's made when the server installs.
      </p>
    );
  }
  if (!parsed.ok) {
    return (
      <div className="flex flex-col gap-2 text-sm">
        <p className="text-destructive">
          {config} can't be read, so its mods can't be changed here. {parsed.error}
        </p>
        {fileLink}
      </div>
    );
  }
  return (
    <ModList
      key={JSON.stringify(parsed.mods)}
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      config={config}
      saved={parsed.mods}
      canWrite={canWrite}
      readText={readText}
      fileLink={fileLink}
    />
  );
}

function ModList({
  orgId,
  nodeId,
  serverId,
  config,
  saved,
  canWrite,
  readText,
  fileLink,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  config: string;
  saved: ConfigMod[];
  canWrite: boolean;
  readText: () => Promise<string | undefined>;
  fileLink: ReactNode;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const [mods, setMods] = useState(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const ids = mods.map((m) => m.modId).filter((id) => /^[0-9A-F]{16}$/.test(id));
  const lookup = useQuery(
    AddonService.method.lookupReforgerMods,
    { orgId, ids: ids.slice(0, 100) },
    { enabled: ids.length > 0 },
  );
  const byId = new Map((lookup.data?.mods ?? []).map((m) => [m.id, m]));
  const changed = !sameMods(mods, saved);
  const have = new Set(mods.map((m) => m.modId));
  // What the listed mods need that isn't listed.
  const missing = new Map<string, string>();
  for (const m of lookup.data?.mods ?? []) {
    for (const d of m.dependencies) if (!have.has(d.id)) missing.set(d.id, d.name);
  }

  async function save() {
    setBusy(true);
    setError("");
    setNote("");
    try {
      // The file as it is now: the Panel and the startup change it at
      // every start, and only the mods are this page's.
      const now = await readText();
      if (now === undefined) throw new Error(`${config} is gone: reinstall the server to make it.`);
      await api.run("files.write", {
        path: config,
        data: base64(new TextEncoder().encode(withMods(now, mods))),
      });
      setNote(`Saved. ${apply}`);
      await client.invalidateQueries({ queryKey: ["reforger-mods", nodeId, serverId, config] });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Workshop mods</CardTitle>
          <CardDescription>
            The mods in {config}. {apply} A mod without a pinned version gets its newest.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 text-sm">
          {lookup.error && (
            <p className="text-muted-foreground">
              {message(lookup.error)} The mods are shown by the names in {config}.
            </p>
          )}
          {mods.length === 0 ? (
            <p className="text-muted-foreground">No mods yet.{canWrite ? " Add one below." : ""}</p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {mods.map((m) => (
                <ModRow
                  key={m.modId}
                  mod={m}
                  found={byId.get(m.modId)}
                  loading={lookup.isPending && ids.length > 0}
                  looked={!!lookup.data}
                  edit={
                    canWrite && !busy
                      ? {
                          remove: () => setMods(removeMod(mods, m.modId)),
                          pin: (v) => setMods(pinMod(mods, m.modId, v)),
                        }
                      : undefined
                  }
                />
              ))}
            </ul>
          )}
          {missing.size > 0 && (
            <div className="flex flex-col gap-2 rounded-lg border border-dashed px-3 py-2.5">
              <p>
                These mods need {[...missing.values()].join(", ")}, which{" "}
                {missing.size === 1 ? "isn't" : "aren't"} listed.
              </p>
              {canWrite && (
                <div>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      setMods(
                        addMods(
                          mods,
                          [...missing].map(([modId, name]) => ({ modId, name })),
                        ),
                      )
                    }
                  >
                    <Plus /> Add {missing.size === 1 ? "it" : "them"}
                  </Button>
                </div>
              )}
            </div>
          )}
          {canWrite && changed && (
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" disabled={busy} onClick={save}>
                {busy && <Loader2 className="animate-spin" />}
                Save
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setMods(saved)}>
                Undo
              </Button>
            </div>
          )}
          {note && <p className="text-muted-foreground">{note}</p>}
          {error && <p className="text-destructive">{error}</p>}
          <div>{fileLink}</div>
        </CardContent>
      </Card>
      {canWrite && (
        <AddMod
          orgId={orgId}
          have={have}
          onAdd={(add) => {
            setNote("");
            setMods(addMods(mods, add));
          }}
        />
      )}
    </div>
  );
}

function Preview({ url }: { url?: string }) {
  return url ? (
    <img
      src={url}
      alt=""
      className="h-9 w-16 shrink-0 rounded-lg border object-cover"
      loading="lazy"
    />
  ) : (
    <span className="flex h-9 w-16 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
      <Package className="size-4.5" />
    </span>
  );
}

function ModRow({
  mod,
  found,
  loading,
  looked,
  edit,
}: {
  mod: ConfigMod;
  found?: ReforgerMod;
  loading: boolean;
  looked: boolean;
  edit?: { remove: () => void; pin: (version: string | undefined) => void };
}) {
  const name = found?.name || mod.name || (loading ? "…" : `Mod ${mod.modId}`);
  const behind = found && mod.version && found.version && mod.version !== found.version;
  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2.5">
      <Preview url={found?.previewUrl} />
      <div className="min-w-0 flex-1 basis-48">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate font-medium">{name}</span>
          {mod.version ? (
            <Badge variant="outline">Pinned to {mod.version}</Badge>
          ) : (
            found?.version && <Badge variant="secondary">Newest: {found.version}</Badge>
          )}
          {behind && <Badge>{found.version} is out</Badge>}
          {looked && !found && <Badge variant="outline">Not on the Workshop, or private</Badge>}
        </div>
        <p className="truncate font-mono text-xs text-muted-foreground">
          {mod.modId}
          {found?.author ? ` · ${found.author}` : ""}
          {found && found.size > 0n ? ` · ${bytes(Number(found.size))}` : ""}
          {found?.updatedAt ? ` · updated ${dateOnly(timestampDate(found.updatedAt))}` : ""}
        </p>
      </div>
      <a
        href={modLink(mod.modId)}
        target="_blank"
        rel="noreferrer"
        aria-label={`Open ${name} on the Workshop`}
        className="text-muted-foreground hover:text-foreground"
      >
        <ExternalLink className="size-4" />
      </a>
      {edit && (
        <>
          {mod.version ? (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Always use ${name}'s newest version`}
              title="Unpin: always use the newest version"
              onClick={() => edit.pin(undefined)}
            >
              <PinOff />
            </Button>
          ) : (
            found?.version && (
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Pin ${name} to ${found.version}`}
                title={`Pin to ${found.version}, so updates don't change it`}
                onClick={() => edit.pin(found.version)}
              >
                <Pin />
              </Button>
            )
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Remove ${name}`}
            className="text-muted-foreground hover:text-destructive"
            onClick={edit.remove}
          >
            <Trash2 />
          </Button>
        </>
      )}
    </li>
  );
}

// AddMod searches the Workshop, or takes a mod's link or ID. A mod comes
// with what it needs. When the Workshop can't say what an ID is, the mod
// is added with a name typed in.
function AddMod({
  orgId,
  have,
  onAdd,
}: {
  orgId: string;
  have: Set<string>;
  onAdd: (mods: ConfigMod[]) => void;
}) {
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [ref, setRef] = useState("");
  const [name, setName] = useState("");
  const [adding, setAdding] = useState("");
  const [error, setError] = useState("");
  const found = useQuery(
    AddonService.method.lookupReforgerMods,
    { orgId, ids: [ref] },
    { enabled: ref !== "" },
  );
  const search = useQuery(
    AddonService.method.searchReforgerMods,
    { orgId, query, page },
    { enabled: query !== "" },
  );
  const item = found.data?.mods[0];

  function look(e: FormEvent) {
    e.preventDefault();
    setError("");
    const id = modRef(input);
    if (id) {
      setRef(id);
      setQuery("");
    } else {
      setRef("");
      setQuery(input.trim());
      setPage(1);
    }
  }

  function done() {
    setInput("");
    setRef("");
    setQuery("");
    setName("");
  }

  // add adds a mod with the mods it needs (theirs too, a few levels).
  async function add(m: ReforgerMod) {
    setAdding(m.id);
    setError("");
    try {
      const out: ConfigMod[] = [{ modId: m.id, name: m.name }];
      let next = m.dependencies.length > 0 ? m.dependencies.map((d) => d.id) : [];
      // A search's mods don't say what they need: look it up.
      if (next.length === 0) {
        const r = await addonClient.lookupReforgerMods({ orgId, ids: [m.id] });
        next = (r.mods[0]?.dependencies ?? []).map((d) => d.id);
      }
      const seen = new Set([m.id, ...have]);
      for (let depth = 0; depth < 4 && next.length > 0; depth++) {
        next = next.filter((id) => !seen.has(id));
        if (next.length === 0) break;
        for (const id of next) seen.add(id);
        const r = await addonClient.lookupReforgerMods({ orgId, ids: next.slice(0, 100) });
        const named = new Map(r.mods.map((x) => [x.id, x]));
        for (const id of next) out.push({ modId: id, name: named.get(id)?.name ?? "" });
        next = r.mods.flatMap((x) => x.dependencies.map((d) => d.id));
      }
      onAdd(out);
      done();
    } catch (err) {
      // The mod itself, without what it needs: better than nothing.
      setError(`${message(err)} It was added without the mods it needs.`);
      onAdd([{ modId: m.id, name: m.name }]);
    } finally {
      setAdding("");
    }
  }

  const unknown = ref && found.data && !item;
  const total = search.data?.total ?? 0;
  const pages = Math.ceil(total / 16);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Add a mod</CardTitle>
        <CardDescription>
          Search the{" "}
          <a
            href="https://reforger.armaplatform.com/workshop"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-3"
          >
            Arma Reforger Workshop
          </a>
          , or paste a mod's link or ID. The mods it needs come with it.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <form onSubmit={look} className="flex gap-2">
          <Input
            aria-label="Search, or a mod's link or ID"
            placeholder="RHS, or https://reforger.armaplatform.com/workshop/…"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={!input.trim()}>
            {modRef(input) ? "Look up" : "Search"}
          </Button>
        </form>
        {error && <p className="text-destructive">{error}</p>}
        {(ref ? found.error : search.error) && (
          <p className="text-destructive">{message(ref ? found.error : search.error)}</p>
        )}
        {ref && found.isPending && <p className="text-muted-foreground">Looking it up…</p>}
        {query && search.isPending && <p className="text-muted-foreground">Searching…</p>}
        {item && (
          <ul className="rounded-lg border">
            <Result mod={item} have={have} adding={adding} onAdd={add} />
          </ul>
        )}
        {(unknown || (ref && found.error)) && (
          // The Workshop doesn't know it (or isn't answering): take a
          // name, since the config wants one.
          <form
            className="flex flex-col gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              onAdd([{ modId: ref, name: name.trim() }]);
              done();
            }}
          >
            <p className="text-muted-foreground">
              {unknown
                ? "The Workshop has no public mod with that ID. If it's right (an unlisted mod), name it and add it anyway."
                : "Name the mod to add it without the Workshop."}
            </p>
            <div className="flex gap-2">
              <Input
                aria-label="Mod name"
                placeholder="Its name"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Button type="submit" variant="outline" disabled={!name.trim() || have.has(ref)}>
                <Plus /> Add {ref}
              </Button>
            </div>
          </form>
        )}
        {query && search.data && (
          <>
            {search.data.mods.length === 0 ? (
              <p className="text-muted-foreground">No mods match {query}.</p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {search.data.mods.map((m) => (
                  <Result key={m.id} mod={m} have={have} adding={adding} onAdd={add} />
                ))}
              </ul>
            )}
            {pages > 1 && (
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={page <= 1}
                  onClick={() => setPage(page - 1)}
                >
                  Previous
                </Button>
                <span className="text-muted-foreground">
                  Page {page} of {pages}
                </span>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={page >= Math.min(pages, 100)}
                  onClick={() => setPage(page + 1)}
                >
                  Next
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function Result({
  mod,
  have,
  adding,
  onAdd,
}: {
  mod: ReforgerMod;
  have: Set<string>;
  adding: string;
  onAdd: (m: ReforgerMod) => void;
}) {
  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2.5">
      <Preview url={mod.previewUrl} />
      <div className="min-w-0 flex-1 basis-48">
        <span className="font-medium">{mod.name}</span>{" "}
        <span className="font-mono text-xs text-muted-foreground">{mod.version}</span>
        <p className="truncate text-xs text-muted-foreground">
          {mod.author ? `${mod.author} · ` : ""}
          {mod.summary}
        </p>
      </div>
      <a
        href={modLink(mod.id)}
        target="_blank"
        rel="noreferrer"
        aria-label={`Open ${mod.name} on the Workshop`}
        className="text-muted-foreground hover:text-foreground"
      >
        <ExternalLink className="size-4" />
      </a>
      {have.has(mod.id) ? (
        <Badge variant="secondary">Added</Badge>
      ) : (
        <Button size="sm" variant="outline" disabled={!!adding} onClick={() => onAdd(mod)}>
          {adding === mod.id ? <Loader2 className="animate-spin" /> : <Plus />} Add
        </Button>
      )}
    </li>
  );
}
