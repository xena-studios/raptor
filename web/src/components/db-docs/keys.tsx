import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import {
  Clock,
  KeyRound,
  Loader2,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
  Timer,
  Trash2,
} from "lucide-react";
import { type FormEvent, lazy, Suspense, useRef, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
import { dbCommand } from "@/lib/db-browser";
import {
  type CommandResult,
  type KeyInfo,
  type KeyItem,
  type KeysResult,
  type KeyValue,
  keysKey,
  refOf,
} from "@/lib/db-documents";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";
import { splitArgs, ttlText } from "@/lib/redis-args";

const MonacoEditor = lazy(() => import("@/components/monaco"));

const typeNames: Record<string, string> = {
  string: "String",
  hash: "Hash",
  list: "List",
  set: "Set",
  zset: "Sorted set",
  stream: "Stream",
  "ReJSON-RL": "JSON",
};

// KeyBrowser is a Redis database's keys: found by pattern on the left,
// one key's value on the right to read and change, and a command line.
export function KeyBrowser({
  nodeId,
  serverId,
  id,
  canWrite,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  canWrite: boolean;
}) {
  const client = useQueryClient();
  const [pattern, setPattern] = useState("");
  const [search, setSearch] = useState("");
  const [picked, setPicked] = useState<KeyInfo>();
  const [tab, setTab] = useState<"key" | "command">("key");
  const [creating, setCreating] = useState(false);

  const keys = useInfiniteQuery({
    queryKey: [...keysKey(nodeId, serverId, id), search],
    queryFn: ({ pageParam }) =>
      dbCommand<KeysResult>(nodeId, serverId, "db.keys", {
        id,
        pattern: search || undefined,
        cursor: pageParam || undefined,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => (last.cursor === "0" ? undefined : last.cursor),
    retry: false,
  });
  const all = keys.data?.pages.flatMap((p) => p.keys) ?? [];
  const total = keys.data?.pages[0]?.total;

  const refresh = () => client.invalidateQueries({ queryKey: keysKey(nodeId, serverId, id) });

  return (
    <div className="grid gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]">
      <aside className="flex min-w-0 flex-col gap-2">
        <form
          className="relative"
          onSubmit={(e) => {
            e.preventDefault();
            setSearch(pattern.trim());
          }}
        >
          <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            aria-label="Find keys"
            placeholder="Keys, like player:*"
            className="pl-8 font-mono"
            value={pattern}
            onChange={(e) => setPattern(e.target.value)}
          />
        </form>
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            {total === undefined
              ? "Reading keys…"
              : `${total.toLocaleString()} ${total === 1 ? "key" : "keys"} in all`}
          </span>
          <button
            type="button"
            className="inline-flex items-center gap-1 hover:text-foreground"
            onClick={refresh}
          >
            <RefreshCw className="size-3" /> Refresh
          </button>
        </div>
        {keys.error && <p className="text-sm text-destructive">{message(keys.error)}</p>}
        {keys.data && all.length === 0 && (
          <p className="text-sm text-muted-foreground">
            {search
              ? "No keys match."
              : "No keys yet. The server's plugins make theirs as they go."}
          </p>
        )}
        <nav aria-label="Keys" className="flex max-h-[60vh] flex-col gap-0.5 overflow-y-auto">
          {all.map((k) => {
            const on = picked && (picked.hex ?? picked.key) === (k.hex ?? k.key);
            return (
              <button
                key={k.hex ?? k.key}
                type="button"
                aria-current={on ? "true" : undefined}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted",
                  on && "bg-brand-wash hover:bg-brand-wash",
                )}
                onClick={() => {
                  setPicked(k);
                  setTab("key");
                }}
              >
                <span className="min-w-0 flex-1 truncate font-mono">{k.key}</span>
                {k.ttl >= 0 && (
                  <Clock
                    className="size-3 shrink-0 text-muted-foreground"
                    aria-label={ttlText(k.ttl)}
                  />
                )}
                <span className="shrink-0 font-mono text-[10px] text-muted-foreground uppercase">
                  {typeNames[k.type] ?? k.type}
                </span>
              </button>
            );
          })}
          {keys.hasNextPage && (
            <Button
              size="sm"
              variant="ghost"
              disabled={keys.isFetchingNextPage}
              onClick={() => void keys.fetchNextPage()}
            >
              {keys.isFetchingNextPage && <Loader2 className="animate-spin" />} More keys
            </Button>
          )}
        </nav>
        {canWrite && (
          <Button
            size="sm"
            variant="outline"
            className="self-start"
            onClick={() => setCreating(true)}
          >
            <Plus /> New key
          </Button>
        )}
      </aside>

      <section className="flex min-w-0 flex-col gap-4">
        <div role="tablist" aria-label="Redis" className="flex items-center gap-1 border-b">
          {(
            [
              ["key", "Key"],
              ["command", "Command"],
            ] as const
          ).map(([t, label]) => (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={tab === t}
              className={cn(
                "-mb-px border-b-2 px-3 py-2 font-mono text-xs tracking-wide uppercase transition-colors",
                tab === t
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
              onClick={() => setTab(t)}
            >
              {label}
            </button>
          ))}
        </div>
        {tab === "key" &&
          (picked ? (
            <KeyView
              key={picked.hex ?? picked.key}
              nodeId={nodeId}
              serverId={serverId}
              id={id}
              k={picked}
              canWrite={canWrite}
              onGone={() => {
                setPicked(undefined);
                void refresh();
              }}
              onRenamed={(to) => {
                setPicked({ ...picked, key: to, hex: undefined });
                void refresh();
              }}
            />
          ) : (
            <p className="text-sm text-muted-foreground">Pick a key to see its value.</p>
          ))}
        {tab === "command" && (
          <CommandTab nodeId={nodeId} serverId={serverId} id={id} canWrite={canWrite} />
        )}
      </section>

      {creating && (
        <NewKeyDialog
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          onDone={(key) => {
            setCreating(false);
            void refresh();
            if (key) setPicked({ key, type: "string", ttl: -1 });
          }}
        />
      )}
    </div>
  );
}

function KeyView({
  nodeId,
  serverId,
  id,
  k,
  canWrite,
  onGone,
  onRenamed,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  k: KeyInfo;
  canWrite: boolean;
  onGone: () => void;
  onRenamed: (to: string) => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const settings = useFileSettings();
  const [cursor, setCursor] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [item, setItem] = useState<{ item?: KeyItem }>();
  // What's been typed in the editor; undefined is nothing yet.
  const [draft, setDraft] = useState<string>();
  const queryKey = [...keysKey(nodeId, serverId, id), "value", k.hex ?? k.key, cursor];
  const v = useQuery({
    queryKey,
    queryFn: () =>
      dbCommand<KeyValue>(nodeId, serverId, "db.key", {
        id,
        ...refOf(k),
        cursor: cursor || undefined,
      }),
    retry: false,
  });

  async function edit(params: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      await dbCommand(nodeId, serverId, "db.key.edit", { id, ...refOf(k), ...params });
      await client.invalidateQueries({ queryKey: keysKey(nodeId, serverId, id) });
      return true;
    } catch (err) {
      setError(message(err));
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function save(value: string) {
    if (await edit({ op: v.data?.type === "ReJSON-RL" ? "json.set" : "set", value }))
      setDraft(undefined);
  }

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${k.key}?`,
      description:
        "The key and its value are deleted. Plugins that use it make it again if they need it.",
      confirm: "Delete key",
      destructive: true,
    });
    if (ok && (await edit({ op: "delete" }))) onGone();
  }

  async function rename() {
    const to = await ask.prompt({ title: `Rename ${k.key}`, label: "New name", initial: k.key });
    if (to && to !== k.key && (await edit({ op: "rename", value: to }))) onRenamed(to);
  }

  async function expire() {
    const secs = await ask.prompt({
      title: `When ${k.key} expires`,
      description: "In seconds from now. Leave it empty to keep it for good.",
      label: "Seconds",
      initial: v.data && v.data.ttl >= 0 ? String(Math.round(v.data.ttl / 1000)) : "",
    });
    if (secs === null) return;
    const n = Number(secs);
    if (secs.trim() !== "" && (!Number.isFinite(n) || n <= 0)) {
      setError("A number of seconds, more than 0.");
      return;
    }
    await edit({ op: "expire", ttl: secs.trim() === "" ? 0 : Math.round(n * 1000) });
  }

  async function removeItem(it: KeyItem) {
    const t = v.data?.type;
    const op = t === "hash" ? "hdel" : t === "list" ? "lrem" : t === "set" ? "srem" : "zrem";
    await edit({ op, field: it.field, value: it.value });
  }

  const d = v.data;
  const editable = canWrite && d && d.type !== "stream";
  const isJSON = d?.type === "ReJSON-RL" || (d?.string !== undefined && looksJSON(d.string));

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <KeyRound className="size-4 text-muted-foreground" />
        <h3 className="min-w-0 truncate font-mono text-sm">{k.key}</h3>
        {d && <Badge variant="secondary">{typeNames[d.type] ?? d.type}</Badge>}
        {d && (
          <span className="text-xs text-muted-foreground">
            {ttlText(d.ttl)} ·{" "}
            {d.type === "string" || d.type === "ReJSON-RL"
              ? `${d.length.toLocaleString()} bytes`
              : `${d.length.toLocaleString()} ${d.length === 1 ? "item" : "items"}`}
          </span>
        )}
        {canWrite && d && (
          <div className="ml-auto flex gap-1">
            <Button size="sm" variant="ghost" disabled={busy} onClick={expire}>
              <Timer /> Expiry
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={rename}>
              <Pencil /> Rename
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={remove}>
              <Trash2 /> Delete
            </Button>
          </div>
        )}
      </div>
      {v.error && <p className="text-sm text-destructive">{message(v.error)}</p>}
      {v.isPending && <p className="text-sm text-muted-foreground">Reading it…</p>}
      {d?.string !== undefined && (
        <>
          <Card className="h-72 overflow-hidden py-0">
            <Suspense
              fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
            >
              <MonacoEditor
                value={isJSON ? prettyJSON(d.string) : d.string}
                language={isJSON ? "json" : "plaintext"}
                readOnly={!editable || !!d.cut}
                settings={settings}
                onChange={setDraft}
                onSave={() => {
                  if (draft !== undefined) void save(draft);
                }}
              />
            </Suspense>
          </Card>
          {d.cut && (
            <p className="text-xs text-muted-foreground">
              It's over 1 MiB, so only the start is shown, and it can't be changed here.
            </p>
          )}
          {editable && !d.cut && (
            <Button
              size="sm"
              className="self-start"
              disabled={busy || draft === undefined}
              onClick={() => {
                if (draft !== undefined) void save(draft);
              }}
            >
              {busy && <Loader2 className="animate-spin" />} Save
            </Button>
          )}
        </>
      )}
      {d?.items && (
        <div className="overflow-hidden rounded-lg border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left font-mono text-xs text-muted-foreground uppercase">
              <tr>
                {d.type === "hash" && <th className="px-3 py-2 font-normal">Field</th>}
                {d.type === "list" && <th className="w-16 px-3 py-2 font-normal">#</th>}
                {d.type === "stream" && <th className="px-3 py-2 font-normal">ID</th>}
                <th className="px-3 py-2 font-normal">
                  {d.type === "stream" ? "Fields" : "Value"}
                </th>
                {d.type === "zset" && (
                  <th className="w-24 px-3 py-2 text-right font-normal">Score</th>
                )}
                {editable && <th className="w-20" />}
              </tr>
            </thead>
            <tbody className="font-mono">
              {d.items.map((it) => (
                <tr
                  // A hash's fields, a list's positions, a set's members,
                  // and a stream's IDs are each unique.
                  key={it.field ?? (it.index !== undefined ? `#${it.index}` : it.value)}
                  className="border-t align-top"
                >
                  {d.type === "hash" && <td className="px-3 py-1.5 break-all">{it.field}</td>}
                  {d.type === "list" && (
                    <td className="px-3 py-1.5 text-muted-foreground">{it.index}</td>
                  )}
                  {d.type === "stream" && (
                    <td className="px-3 py-1.5 whitespace-nowrap">{it.field}</td>
                  )}
                  <td className="px-3 py-1.5 break-all whitespace-pre-wrap">
                    {d.type === "stream"
                      ? Object.entries(it.fields ?? {})
                          .map(([f, val]) => `${f}: ${val}`)
                          .join("\n")
                      : it.value}
                  </td>
                  {d.type === "zset" && (
                    <td className="px-3 py-1.5 text-right tabular-nums">{it.score}</td>
                  )}
                  {editable && (
                    <td className="px-1 py-1 text-right whitespace-nowrap">
                      {d.type !== "set" && (
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label="Change"
                          onClick={() => setItem({ item: it })}
                        >
                          <Pencil />
                        </Button>
                      )}
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label="Remove"
                        disabled={busy}
                        onClick={() => void removeItem(it)}
                      >
                        <Trash2 />
                      </Button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {d && (
        <div className="flex flex-wrap gap-2">
          {editable && d.items && (
            <Button size="sm" variant="outline" onClick={() => setItem({})}>
              <Plus /> Add
            </Button>
          )}
          {cursor && (
            <Button size="sm" variant="ghost" onClick={() => setCursor("")}>
              From the start
            </Button>
          )}
          {d.cursor && (
            <Button size="sm" variant="ghost" onClick={() => setCursor(d.cursor ?? "")}>
              More
            </Button>
          )}
        </div>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
      {item && d && (
        <ItemDialog
          type={d.type}
          item={item.item}
          onSubmit={async (p) => {
            if (await edit(p)) setItem(undefined);
          }}
          onClose={() => setItem(undefined)}
        />
      )}
    </div>
  );
}

// ItemDialog adds an element to a hash, list, set, or sorted set, or
// changes one.
function ItemDialog({
  type,
  item,
  onSubmit,
  onClose,
}: {
  type: string;
  item?: KeyItem;
  onSubmit: (params: Record<string, unknown>) => Promise<void>;
  onClose: () => void;
}) {
  const [field, setField] = useState(item?.field ?? "");
  const [value, setValue] = useState(item?.value ?? "");
  const [score, setScore] = useState(item?.score !== undefined ? String(item.score) : "0");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const p: Record<string, unknown> =
      type === "hash"
        ? { op: "hset", field, value }
        : type === "list"
          ? item?.index !== undefined
            ? { op: "lset", index: item.index, value }
            : { op: "rpush", value }
          : type === "set"
            ? { op: "sadd", value }
            : { op: "zadd", value, score: Number(score) };
    await onSubmit(p);
    setBusy(false);
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{item ? "Change it" : "Add one"}</DialogTitle>
            <DialogDescription>
              {type === "list" && !item ? "It's added at the end of the list." : ""}
            </DialogDescription>
          </DialogHeader>
          {type === "hash" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="item-field">Field</Label>
              <Input
                id="item-field"
                className="font-mono"
                required
                readOnly={!!item}
                value={field}
                onChange={(e) => setField(e.target.value)}
              />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="item-value">Value</Label>
            <Input
              id="item-value"
              className="font-mono"
              autoFocus
              value={value}
              readOnly={type === "zset" && !!item}
              onChange={(e) => setValue(e.target.value)}
            />
          </div>
          {type === "zset" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="item-score">Score</Label>
              <Input
                id="item-score"
                type="number"
                step="any"
                required
                value={score}
                onChange={(e) => setScore(e.target.value)}
              />
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />} Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// NewKeyDialog makes a string key; other types are made by adding to them
// (a hash by setting a field, and so on), here or from the command line.
function NewKeyDialog({
  nodeId,
  serverId,
  id,
  onDone,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  onDone: (key?: string) => void;
}) {
  const [type, setType] = useState("string");
  const [key, setKey] = useState("");
  const [field, setField] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const op = { string: "set", hash: "hset", list: "rpush", set: "sadd", zset: "zadd" }[type];
    try {
      const exists = await dbCommand<CommandResult>(nodeId, serverId, "db.redis.command.read", {
        id,
        args: ["EXISTS", key],
      });
      if (exists.reply === 1) {
        setError("There's already a key with that name.");
        return;
      }
      await dbCommand(nodeId, serverId, "db.key.edit", {
        id,
        key,
        op,
        field: type === "hash" ? field : undefined,
        value,
        score: type === "zset" ? 0 : undefined,
      });
      onDone(key);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onDone()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>New key</DialogTitle>
            <DialogDescription>It's made with its first value.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-[1fr_9rem]">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-key">Name</Label>
              <Input
                id="new-key"
                className="font-mono"
                autoFocus
                required
                value={key}
                onChange={(e) => setKey(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-key-type">Type</Label>
              <select
                id="new-key-type"
                className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm"
                value={type}
                onChange={(e) => setType(e.target.value)}
              >
                {["string", "hash", "list", "set", "zset"].map((t) => (
                  <option key={t} value={t}>
                    {typeNames[t]}
                  </option>
                ))}
              </select>
            </div>
          </div>
          {type === "hash" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-key-field">First field</Label>
              <Input
                id="new-key-field"
                className="font-mono"
                required
                value={field}
                onChange={(e) => setField(e.target.value)}
              />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-key-value">{type === "string" ? "Value" : "First value"}</Label>
            <Input
              id="new-key-value"
              className="font-mono"
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onDone()}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !key}>
              {busy && <Loader2 className="animate-spin" />} Make key
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// CommandTab runs Redis commands as the database's user, like redis-cli.
// Those who may only look run read-only ones.
function CommandTab({
  nodeId,
  serverId,
  id,
  canWrite,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  canWrite: boolean;
}) {
  const client = useQueryClient();
  const [line, setLine] = useState("");
  const [busy, setBusy] = useState(false);
  const [history, setHistory] = useState<
    { n: number; line: string; out: string; error?: boolean }[]
  >([]);
  const seq = useRef(0);

  async function run(e: FormEvent) {
    e.preventDefault();
    const args = splitArgs(line);
    if (args.length === 0) return;
    setBusy(true);
    try {
      const res = await dbCommand<CommandResult>(
        nodeId,
        serverId,
        canWrite ? "db.redis.command" : "db.redis.command.read",
        { id, args },
      );
      setHistory((h) => [{ n: ++seq.current, line, out: replyText(res) }, ...h].slice(0, 50));
      setLine("");
      if (canWrite) await client.invalidateQueries({ queryKey: keysKey(nodeId, serverId, id) });
    } catch (err) {
      setHistory((h) =>
        [{ n: ++seq.current, line, out: message(err), error: true }, ...h].slice(0, 50),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <form onSubmit={run} className="flex gap-2">
        <Input
          aria-label="Command"
          className="font-mono"
          placeholder={canWrite ? "HGETALL player:alex" : "GET player:alex"}
          spellCheck={false}
          autoComplete="off"
          value={line}
          onChange={(e) => setLine(e.target.value)}
        />
        <Button type="submit" disabled={busy || !line.trim()}>
          {busy ? <Loader2 className="animate-spin" /> : <Play />} Run
        </Button>
      </form>
      <p className="text-xs text-muted-foreground">
        {canWrite
          ? "As the database's own user, which can't change Redis' settings or reach anything else. Commands that wait (BLPOP, SUBSCRIBE) aren't run here."
          : "You can look, not change: only read-only commands run."}
      </p>
      <div className="flex flex-col gap-2">
        {history.map((h) => (
          <div key={h.n} className="rounded-lg border bg-muted/30 p-3 font-mono text-xs">
            <p className="text-muted-foreground">&gt; {h.line}</p>
            <pre
              className={cn("mt-1 break-all whitespace-pre-wrap", h.error && "text-destructive")}
            >
              {h.out}
            </pre>
          </div>
        ))}
      </div>
    </div>
  );
}

function replyText(r: CommandResult): string {
  const v = r.reply;
  const body =
    v === null || v === undefined
      ? "(nil)"
      : typeof v === "string"
        ? `"${v}"`
        : JSON.stringify(v, null, 2);
  return `${body}${r.cut ? "\n(cut short)" : ""}\n(${r.millis} ms)`;
}

function looksJSON(s: string): boolean {
  const t = s.trimStart();
  if (!t.startsWith("{") && !t.startsWith("[")) return false;
  try {
    JSON.parse(s);
    return true;
  } catch {
    return false;
  }
}

function prettyJSON(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
