import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Layers, Loader2, Package, Plus, Trash2 } from "lucide-react";
import { type FormEvent, useMemo, useState } from "react";

import { EmptyState } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AddonService, type WorkshopItem } from "@/gen/raptor/panel/v1/addon_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { formatBytes as bytes } from "@/lib/metrics";
import {
  type ServerConfig,
  updateParams,
  type Workshop,
  workshopIds,
  workshopRef,
} from "@/lib/servers";
import { commandClient } from "@/lib/transport";

// Steam Workshop items (docs/EGGS.md#steam-workshop): the egg names the
// variable the game reads them from, and the game (or its install script)
// downloads them. This page only edits that variable, naming the IDs from
// Steam through the Panel.

export function WorkshopPage({
  orgId,
  nodeId,
  serverId,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const ws = cfg?.egg?.workshop;
  if (res.error) return <p className="text-sm text-destructive">{message(res.error)}</p>;
  if (!cfg) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (!ws) {
    return (
      <EmptyState
        icon={Package}
        title="This server's game doesn't take Steam Workshop items"
        description="Only games that download Workshop items themselves have this page."
      />
    );
  }
  const variable = cfg.egg?.variables?.find((v) => v.env === ws.variable);
  const value = cfg.variables?.[ws.variable] ?? variable?.default ?? "";
  return (
    <WorkshopItems
      key={value}
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      cfg={cfg}
      name={res.data?.server?.name ?? ""}
      ws={ws}
      saved={workshopIds(value)}
      canWrite={canWrite}
    />
  );
}

function Preview({ url, collection }: { url?: string; collection?: boolean }) {
  return url ? (
    <img
      src={url}
      alt=""
      className="size-9 shrink-0 rounded-lg border object-cover"
      loading="lazy"
    />
  ) : (
    <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
      {collection ? <Layers className="size-4.5" /> : <Package className="size-4.5" />}
    </span>
  );
}

function itemLink(id: string) {
  return `https://steamcommunity.com/sharedfiles/filedetails/?id=${id}`;
}

function WorkshopItems({
  orgId,
  nodeId,
  serverId,
  cfg,
  name,
  ws,
  saved,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  cfg: ServerConfig;
  name: string;
  ws: Workshop;
  saved: string[];
  canWrite: boolean;
}) {
  const client = useQueryClient();
  const [ids, setIds] = useState(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const lookup = useQuery(
    AddonService.method.lookupWorkshopItems,
    { orgId, ids: ids.slice(0, 100) },
    { enabled: ids.length > 0 },
  );
  const byId = new Map((lookup.data?.items ?? []).map((i) => [i.id, i]));
  const collection = ws.collection ? byId.get(ids[0] ?? "") : undefined;
  // A collection's items, named too.
  const children = useQuery(
    AddonService.method.lookupWorkshopItems,
    { orgId, ids: collection?.children.slice(0, 100) ?? [] },
    { enabled: (collection?.children.length ?? 0) > 0 },
  );
  const changed = ids.join(",") !== saved.join(",");
  const what = ws.collection ? "collection" : "items";
  const apply =
    ws.apply === "reinstall"
      ? "The game downloads them when it's reinstalled, from Settings."
      : "The game downloads them the next time it starts.";

  async function save() {
    setBusy(true);
    setError("");
    setNote("");
    try {
      const params = updateParams(cfg, {
        name,
        image: cfg.image,
        variables: { [ws.variable]: ids.join(",") },
        memoryMiB: cfg.limits.memory_mib,
        diskMiB: cfg.limits.disk_mib,
        port: (cfg.allocations.find((a) => a.primary) ?? cfg.allocations[0])?.port ?? 0,
      });
      await commandClient.execute({
        nodeId,
        action: "server.update",
        serverId,
        paramsJson: JSON.stringify(params),
      });
      setNote(`Saved. ${apply}`);
      await client.invalidateQueries();
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
          <CardTitle>{ws.collection ? "Workshop collection" : "Workshop items"}</CardTitle>
          <CardDescription>
            {ws.collection
              ? "The game loads every item in one Steam Workshop collection. "
              : "The game loads these Steam Workshop items, in this order. "}
            {apply}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {lookup.error && <p className="text-sm text-destructive">{message(lookup.error)}</p>}
          {ids.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No {what} yet.{canWrite ? " Add one below." : ""}
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
                  onRemove={
                    canWrite && !busy ? () => setIds(ids.filter((x) => x !== id)) : undefined
                  }
                />
              ))}
            </ul>
          )}
          {collection && (
            <div className="flex flex-col gap-1.5">
              <Label>Its {collection.children.length} items</Label>
              <ul className="max-h-72 divide-y overflow-y-auto rounded-lg border">
                {collection.children.map((id) => (
                  <ItemRow
                    key={id}
                    id={id}
                    item={children.data?.items.find((i) => i.id === id)}
                    loading={children.isPending}
                    appId={ws.app_id}
                  />
                ))}
              </ul>
            </div>
          )}
          {canWrite && changed && (
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" disabled={busy} onClick={save}>
                {busy && <Loader2 className="animate-spin" />}
                Save
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setIds(saved)}>
                Undo
              </Button>
            </div>
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
            setIds(
              ws.collection
                ? added.slice(0, 1)
                : [...ids, ...added.filter((id) => !ids.includes(id))],
            );
          }}
        />
      )}
    </div>
  );
}

function ItemRow({
  id,
  item,
  loading,
  appId,
  onRemove,
}: {
  id: string;
  item?: WorkshopItem;
  loading: boolean;
  appId: number;
  onRemove?: () => void;
}) {
  const other = item && Number(item.appId) !== appId;
  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2.5">
      <Preview url={item?.previewUrl} collection={item?.collection} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium">
            {item?.title || (loading ? "…" : `Item ${id}`)}
          </span>
          {item?.collection && <Badge variant="outline">Collection</Badge>}
          {other && <Badge variant="destructive">For another game</Badge>}
          {!item && !loading && <Badge variant="outline">Not on Steam, or not public</Badge>}
        </div>
        <p className="truncate font-mono text-xs text-muted-foreground">
          {id}
          {item && !item.collection && item.size > 0n ? ` · ${bytes(Number(item.size))}` : ""}
          {item?.updatedAt ? ` · updated ${dateOnly(timestampDate(item.updatedAt))}` : ""}
        </p>
      </div>
      <a
        href={itemLink(id)}
        target="_blank"
        rel="noreferrer"
        aria-label={`Open ${item?.title || id} on Steam`}
        className="text-muted-foreground hover:text-foreground"
      >
        <ExternalLink className="size-4" />
      </a>
      {onRemove && (
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`Remove ${item?.title || id}`}
          className="text-muted-foreground hover:text-destructive"
          onClick={onRemove}
        >
          <Trash2 />
        </Button>
      )}
    </li>
  );
}

// AddItem takes a Workshop link or ID, shows what it is, and adds it if
// it's for this game (a collection's items, for games that take a list).
function AddItem({
  orgId,
  ws,
  ids,
  onAdd,
}: {
  orgId: string;
  ws: Workshop;
  ids: string[];
  onAdd: (ids: string[]) => void;
}) {
  const [input, setInput] = useState("");
  const [ref, setRef] = useState("");
  const [error, setError] = useState("");
  const found = useQuery(
    AddonService.method.lookupWorkshopItems,
    { orgId, ids: [ref] },
    { enabled: ref !== "" },
  );
  const item = found.data?.items[0];

  function look(e: FormEvent) {
    e.preventDefault();
    const id = workshopRef(input);
    setError(id ? "" : "Paste a Workshop item's link, or the number at its end.");
    setRef(id ?? "");
  }

  let problem = "";
  if (ref && found.data && !item) problem = "Steam has no public item with that ID.";
  else if (item && Number(item.appId) !== ws.app_id) problem = "That's for another game.";
  else if (item && ws.collection && !item.collection)
    problem =
      "This game takes one collection. Make a collection with the item on Steam, and add that.";
  else if (item && ids.includes(item.id)) problem = "It's already added.";
  const expand = item && !ws.collection && item.collection;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{ws.collection ? "Use a collection" : "Add an item"}</CardTitle>
        <CardDescription>
          Paste a link from the{" "}
          <a
            href={`https://steamcommunity.com/app/${ws.app_id}/workshop/`}
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-3"
          >
            game's Steam Workshop
          </a>
          .
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <form onSubmit={look} className="flex gap-2">
          <Input
            aria-label="Workshop link or ID"
            placeholder="https://steamcommunity.com/sharedfiles/filedetails/?id=…"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={!input.trim()}>
            Look up
          </Button>
        </form>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {found.error && <p className="text-sm text-destructive">{message(found.error)}</p>}
        {ref && found.isPending && <p className="text-sm text-muted-foreground">Looking it up…</p>}
        {item && (
          <ul className="rounded-lg border">
            <ItemRow id={item.id} item={item} loading={false} appId={ws.app_id} />
          </ul>
        )}
        {problem && <p className="text-sm text-destructive">{problem}</p>}
        {item && !problem && (
          <div className="flex flex-wrap gap-2">
            {expand ? (
              <Button
                size="sm"
                onClick={() => {
                  onAdd(item.children);
                  setInput("");
                  setRef("");
                }}
              >
                <Plus />
                Add its {item.children.length} items
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={() => {
                  onAdd([item.id]);
                  setInput("");
                  setRef("");
                }}
              >
                <Plus />
                {ws.collection ? "Use this collection" : "Add"}
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
