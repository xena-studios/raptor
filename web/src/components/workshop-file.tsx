import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { type Content, type Entry, useFilesApi } from "@/components/file-manager/data";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { AddItem, ItemRow } from "@/components/workshop";
import { AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { base64, type ServerConfig, type Workshop, type WorkshopFile } from "@/lib/servers";
import {
  dstOverrideIds,
  dstSetupIds,
  iniList,
  jsonIds,
  modInfoId,
  setDstOverrides,
  setDstSetup,
  setIniList,
  setJsonIds,
  workshopPath,
  zomboidModId,
} from "@/lib/workshop-files";

// WorkshopFilePage is the Workshop for games that list their items in a
// config file (x-raptor.workshop.file, #408): the file is read and
// written in place through files.read and files.write, like the file
// manager, so it takes the same permissions. The game still downloads
// the items; Raptor downloads nothing.

type Api = ReturnType<typeof useFilesApi>;
type Lists = { items: string[]; mods?: string[] };

// The files the format touches: the list's, then dst's modoverrides.lua.
function filesOf(file: WorkshopFile, value: (env: string) => string): string[] | undefined {
  const paths = [file.path, ...(file.format === "dst" ? (file.overrides ?? []) : [])].map((p) =>
    workshopPath(p, value),
  );
  return paths.every((p) => p !== undefined) ? (paths as string[]) : undefined;
}

// A file's text; undefined when it isn't there (or can't be read).
async function readText(api: Api, path: string): Promise<string | undefined> {
  try {
    const f = await api.run<Content>("files.read", { path });
    return f ? new TextDecoder().decode(fromBase64(f.data ?? "")) : undefined;
  } catch {
    return undefined;
  }
}

function listsOf(file: WorkshopFile, texts: (string | undefined)[]): Lists {
  const sep = file.separator || ";";
  const [main = "", ...rest] = texts;
  switch (file.format) {
    case "ini":
      return {
        items: (iniList(main, file.key ?? "", sep) ?? []).filter((id) => /^[0-9]+$/.test(id)),
        mods: file.mods_key ? (iniList(main, file.mods_key, sep) ?? []) : undefined,
      };
    case "json":
      return { items: jsonIds(main, file.key ?? "") ?? [] };
    case "dst": {
      // Items turned on in a shard but not downloaded are listed too, so
      // saving puts them in both.
      const all = [...dstSetupIds(main), ...rest.flatMap((t) => dstOverrideIds(t ?? ""))];
      return { items: all.filter((id, i) => all.indexOf(id) === i) };
    }
  }
}

// write gives each file its new text, from what's in it now.
function written(file: WorkshopFile, path: number, text: string | undefined, l: Lists): string {
  const sep = file.separator || ";";
  const t = text ?? "";
  switch (file.format) {
    case "ini": {
      let out = setIniList(t, file.key ?? "", l.items, sep);
      if (file.mods_key && l.mods) out = setIniList(out, file.mods_key, l.mods, sep);
      return out;
    }
    case "json":
      return setJsonIds(t, file.key ?? "", l.items);
    case "dst":
      return path === 0 ? setDstSetup(t, l.items) : setDstOverrides(t, l.items);
  }
}

export function WorkshopFilePage({
  orgId,
  nodeId,
  serverId,
  cfg,
  ws,
  file,
  canRead,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  cfg: ServerConfig;
  ws: Workshop;
  file: WorkshopFile;
  canRead: boolean;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const value = (env: string) =>
    cfg.variables?.[env] ?? cfg.egg?.variables?.find((v) => v.env === env)?.default ?? "";
  const paths = filesOf(file, value);
  const texts = useTanQuery({
    queryKey: ["workshop-file", nodeId, serverId, ...(paths ?? [])],
    queryFn: () => Promise.all((paths ?? []).map((p) => readText(api, p))),
    enabled: canRead && paths !== undefined,
    refetchOnWindowFocus: false,
  });
  if (!canRead) {
    return (
      <p className="text-sm text-muted-foreground">
        This game lists its Workshop items in a file. Seeing them needs the See files permission.
      </p>
    );
  }
  if (!paths) {
    return (
      <p className="text-sm text-destructive">
        The file's name comes from a setting that's empty or isn't a plain name ({file.path}). Fix
        it in Startup.
      </p>
    );
  }
  if (texts.isPending) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (texts.error) return <p className="text-sm text-destructive">{message(texts.error)}</p>;
  const [main] = texts.data;
  // The game makes ini and JSON configs on its first start, with settings
  // Raptor doesn't know, so they aren't made here. dst's are made.
  if (main === undefined && file.format !== "dst") {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Workshop items</CardTitle>
          <CardDescription>
            This game lists its Workshop items in {paths[0]}, which isn't there yet. Start the
            server once: the game makes it, and its items show here.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }
  let lists: Lists;
  try {
    lists = listsOf(file, texts.data);
  } catch (err) {
    return (
      <p className="text-sm text-destructive">
        Raptor can't read the items in {paths.join(" or ")}: {message(err)}. Edit it in Files.
      </p>
    );
  }
  return (
    <FileItems
      key={texts.data.join("\0")}
      orgId={orgId}
      api={api}
      ws={ws}
      file={file}
      paths={paths}
      saved={lists}
      canWrite={canWrite}
    />
  );
}

function FileItems({
  orgId,
  api,
  ws,
  file,
  paths,
  saved,
  canWrite,
}: {
  orgId: string;
  api: Api;
  ws: Workshop;
  file: WorkshopFile;
  paths: string[];
  saved: Lists;
  canWrite: boolean;
}) {
  const client = useQueryClient();
  const [lists, setLists] = useState<Lists>(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const ids = lists.items;
  const lookup = useQuery(
    AddonService.method.lookupWorkshopItems,
    { orgId, ids: ids.slice(0, 100) },
    { enabled: ids.length > 0 },
  );
  const byId = new Map((lookup.data?.items ?? []).map((i) => [i.id, i]));
  const found = useModInfo(api, ws.app_id, saved.items, saved.mods !== undefined);
  const changed = JSON.stringify(lists) !== JSON.stringify(saved);
  const apply =
    ws.apply === "reinstall"
      ? "The game downloads them when it's reinstalled, from Settings."
      : "The game downloads them the next time it starts.";

  function remove(id: string) {
    // The item's own mod IDs go with it, when the game has downloaded it.
    const its = found.data?.get(id) ?? [];
    setLists({
      items: ids.filter((x) => x !== id),
      mods: lists.mods?.filter((m) => !its.includes(m.replace(/^\\+/, ""))),
    });
  }

  async function save() {
    setBusy(true);
    setError("");
    setNote("");
    try {
      // From what's in each file now, so a change made since (by the game
      // or in Files) isn't lost. modoverrides.lua first: a setup line
      // without it downloads an item the game doesn't turn on.
      const order = paths.map((_, i) => i).reverse();
      for (const i of order) {
        const path = paths[i] ?? "";
        const now = await readText(api, path);
        if (now === undefined && file.format !== "dst") {
          throw new Error(`${path} isn't there any more. Start the server once to make it.`);
        }
        const next = written(file, i, now, lists);
        if (next === now) continue;
        await api.run("files.write", { path, data: base64(new TextEncoder().encode(next)) });
      }
      setNote(`Saved. ${apply}`);
      await client.invalidateQueries({ queryKey: ["workshop-file"] });
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
          <CardTitle>Workshop items</CardTitle>
          <CardDescription>
            The game loads these Steam Workshop items, listed in{" "}
            <span className="font-mono">{paths.join(", ")}</span>. {apply}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {lookup.error && <p className="text-sm text-destructive">{message(lookup.error)}</p>}
          {ids.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No items yet.{canWrite ? " Add one below." : ""}
            </p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {ids.map((id) => (
                <ItemRow
                  key={id}
                  id={id}
                  item={byId.get(id)}
                  loading={lookup.isPending}
                  appId={ws.app_id}
                  onRemove={canWrite && !busy ? () => remove(id) : undefined}
                />
              ))}
            </ul>
          )}
          {lists.mods && (
            <ModIds
              mods={lists.mods}
              found={found.data}
              titles={byId}
              canWrite={canWrite && !busy}
              onChange={(mods) => setLists({ ...lists, mods })}
            />
          )}
          {canWrite && changed && (
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" disabled={busy} onClick={save}>
                {busy && <Loader2 className="animate-spin" />}
                Save
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setLists(saved)}>
                Undo
              </Button>
            </div>
          )}
          {!canWrite && (
            <p className="text-sm text-muted-foreground">
              Changing them needs the Change files permission.
            </p>
          )}
          {note && <p className="text-sm text-muted-foreground">{note}</p>}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
      </Card>
      {canWrite && (
        <AddItem
          orgId={orgId}
          ws={ws}
          ids={ids}
          onAdd={(added) => {
            setNote("");
            setLists({ ...lists, items: [...ids, ...added.filter((id) => !ids.includes(id))] });
          }}
        />
      )}
    </div>
  );
}

// useModInfo is each downloaded item's mod IDs, from its mod.info
// (steamapps/workshop/content/<app>/<id>/mods/<mod>/mod.info, or the
// mod's 42/ folder for Build 42). Items the game hasn't downloaded yet
// have none, so a new item's mods show after its first start.
function useModInfo(api: Api, appId: number, ids: string[], enabled: boolean) {
  return useTanQuery({
    queryKey: ["workshop-modinfo", ...api.listKey(""), appId, ...ids],
    enabled: enabled && ids.length > 0,
    refetchOnWindowFocus: false,
    queryFn: async () => {
      const out = new Map<string, string[]>();
      await Promise.all(
        ids.slice(0, 50).map(async (id) => {
          const dir = `steamapps/workshop/content/${appId}/${id}/mods`;
          const list = await api
            .run<{ entries: Entry[] }>("files.list", { path: dir })
            .catch(() => undefined);
          const mods = (list?.entries ?? []).filter((e) => e.type === "dir");
          const found: string[] = [];
          for (const m of mods) {
            for (const f of [`${dir}/${m.name}/mod.info`, `${dir}/${m.name}/42/mod.info`]) {
              const text = await readText(api, f);
              const mid = text && modInfoId(text);
              if (mid) {
                if (!found.includes(mid)) found.push(mid);
                break;
              }
            }
          }
          if (found.length) out.set(id, found);
        }),
      );
      return out;
    },
  });
}

// ModIds is Project Zomboid's second list: the mods to load, by the mod
// IDs in each item's mod.info, which aren't the items' IDs. Those found in
// downloaded items are a click away; any can be typed.
function ModIds({
  mods,
  found,
  titles,
  canWrite,
  onChange,
}: {
  mods: string[];
  found?: Map<string, string[]>;
  titles: Map<string, { title: string }>;
  canWrite: boolean;
  onChange: (mods: string[]) => void;
}) {
  const [input, setInput] = useState("");
  const bare = (m: string) => m.replace(/^\\+/, "");
  const has = new Set(mods.map(bare));
  const offered = [...(found ?? [])].flatMap(([item, ids]) =>
    ids.filter((m) => !has.has(m)).map((m) => ({ item, m })),
  );

  function add(e: FormEvent) {
    e.preventDefault();
    const m = input.trim();
    if (!m || has.has(bare(m)) || /[;\s]/.test(m)) return;
    onChange([...mods, zomboidModId(m, mods)]);
    setInput("");
  }

  return (
    <div className="flex flex-col gap-2 rounded-lg border p-3">
      <div>
        <p className="text-sm font-medium">Mods to load</p>
        <p className="text-sm text-muted-foreground">
          Each item holds one or more mods, loaded by their mod ID. Mods from items the game has
          downloaded show here to add; a new item's show after the server's next start.
        </p>
      </div>
      {mods.length === 0 ? (
        <p className="text-sm text-muted-foreground">No mods loaded.</p>
      ) : (
        <ul className="flex flex-wrap gap-1.5">
          {mods.map((m) => (
            <li key={m}>
              <Badge variant="secondary" className="gap-1 font-mono">
                {bare(m)}
                {canWrite && (
                  <button
                    type="button"
                    aria-label={`Don't load ${bare(m)}`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => onChange(mods.filter((x) => x !== m))}
                  >
                    <Trash2 className="size-3" />
                  </button>
                )}
              </Badge>
            </li>
          ))}
        </ul>
      )}
      {canWrite && offered.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-sm text-muted-foreground">Found in the items:</span>
          {offered.map(({ item, m }) => (
            <Button
              key={`${item}/${m}`}
              size="sm"
              variant="outline"
              title={titles.get(item)?.title}
              onClick={() => onChange([...mods, zomboidModId(m, mods)])}
            >
              <Plus />
              {m}
            </Button>
          ))}
        </div>
      )}
      {canWrite && (
        <form onSubmit={add} className="flex gap-2">
          <Input
            aria-label="Mod ID"
            placeholder="A mod ID, from the item's Steam page"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={!input.trim()}>
            Add
          </Button>
        </form>
      )}
    </div>
  );
}
