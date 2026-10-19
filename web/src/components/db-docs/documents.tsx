import { useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import {
  ChevronLeft,
  ChevronRight,
  FileJson,
  Loader2,
  Pencil,
  Play,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
import { type FormEvent, lazy, Suspense, useState } from "react";

import { useConfirm } from "@/components/confirm";
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
import { dbCommand } from "@/lib/db-browser";
import {
  type Collection,
  collectionsKey,
  type DocumentsResult,
  docID,
  pretty,
  useCollections,
} from "@/lib/db-documents";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";

const MonacoEditor = lazy(() => import("@/components/monaco"));

const pageSize = 50;

// DocumentBrowser is a MongoDB database's collections on the left and a
// collection's documents on the right: found with a filter, sorted, read,
// and changed as JSON (MongoDB's Extended JSON, so IDs and dates come back
// as they went: {"$oid": …}, {"$date": …}).
export function DocumentBrowser({
  nodeId,
  serverId,
  id,
  canWrite,
  onCollection,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  canWrite: boolean;
  // The collection picked, for exporting one.
  onCollection?: (name?: string) => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const cols = useCollections(nodeId, serverId, id);
  const [picked, setPicked] = useState<string>();
  const [error, setError] = useState("");
  const list = cols.data ?? [];
  const coll = list.find((c) => c.name === picked) ?? list[0];

  async function create() {
    const name = await ask.prompt({
      title: "New collection",
      label: "Name",
      description: "Letters, digits, and _ . -. Plugins make theirs when they first write.",
      maxLength: 120,
    });
    if (!name) return;
    setError("");
    try {
      await dbCommand(nodeId, serverId, "db.collection.edit", {
        id,
        op: "create",
        collection: name,
      });
      await client.invalidateQueries({ queryKey: collectionsKey(nodeId, serverId, id) });
      setPicked(name);
      onCollection?.(name);
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[14rem_minmax(0,1fr)]">
      <aside className="flex min-w-0 flex-col gap-2">
        {cols.isPending && <p className="text-sm text-muted-foreground">Reading collections…</p>}
        {cols.error && <p className="text-sm text-destructive">{message(cols.error)}</p>}
        {cols.data && list.length === 0 && (
          <p className="text-sm text-muted-foreground">
            No collections yet. Bots and plugins make theirs when they first write.
          </p>
        )}
        <nav
          aria-label="Collections"
          className="flex max-h-[60vh] flex-col gap-0.5 overflow-y-auto"
        >
          {list.map((c) => {
            const on = coll?.name === c.name;
            return (
              <button
                key={c.name}
                type="button"
                aria-current={on ? "true" : undefined}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted",
                  on && "bg-brand-wash hover:bg-brand-wash",
                )}
                onClick={() => {
                  setPicked(c.name);
                  onCollection?.(c.name);
                }}
              >
                <FileJson className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate font-mono">{c.name}</span>
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  {c.count.toLocaleString()}
                </span>
              </button>
            );
          })}
        </nav>
        {canWrite && (
          <Button size="sm" variant="outline" className="self-start" onClick={create}>
            <Plus /> New collection
          </Button>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </aside>
      <section className="min-w-0">
        {coll ? (
          <CollectionView
            key={coll.name}
            nodeId={nodeId}
            serverId={serverId}
            id={id}
            coll={coll}
            canWrite={canWrite}
            onDropped={() => {
              setPicked(undefined);
              onCollection?.(undefined);
            }}
          />
        ) : (
          cols.data && <p className="text-sm text-muted-foreground">Pick a collection.</p>
        )}
      </section>
    </div>
  );
}

function CollectionView({
  nodeId,
  serverId,
  id,
  coll,
  canWrite,
  onDropped,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  coll: Collection;
  canWrite: boolean;
  onDropped: () => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState("");
  const [query, setQuery] = useState({ filter: "", sort: "" });
  const [skip, setSkip] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<{ doc?: string }>();
  const [error, setError] = useState("");
  const docsKey = [...collectionsKey(nodeId, serverId, id), coll.name, query, skip];
  const docs = useQuery({
    queryKey: docsKey,
    queryFn: () =>
      dbCommand<DocumentsResult>(nodeId, serverId, "db.documents", {
        id,
        collection: coll.name,
        filter: query.filter || undefined,
        sort: query.sort || undefined,
        skip,
        limit: pageSize,
      }),
    retry: false,
  });

  function run(e: FormEvent) {
    e.preventDefault();
    setSkip(0);
    setSelected(new Set());
    setQuery({ filter: filter.trim(), sort: sort.trim() });
  }

  async function changed() {
    await client.invalidateQueries({ queryKey: collectionsKey(nodeId, serverId, id) });
  }

  async function removeSelected() {
    const n = selected.size;
    const ok = await ask.confirm({
      title: `Delete ${n} ${n === 1 ? "document" : "documents"}?`,
      description: "They're deleted from the collection. Your backups still have them.",
      confirm: "Delete",
      destructive: true,
    });
    if (!ok) return;
    setError("");
    try {
      await dbCommand(nodeId, serverId, "db.document.edit", {
        id,
        collection: coll.name,
        op: "delete",
        doc_ids: [...selected],
      });
      setSelected(new Set());
      await changed();
    } catch (err) {
      setError(message(err));
    }
  }

  async function drop() {
    const ok = await ask.confirm({
      title: `Drop ${coll.name}?`,
      description: `Its ${coll.count.toLocaleString()} documents and its indexes are deleted. Your backups still have them.`,
      typeToConfirm: coll.name,
      confirm: "Drop collection",
      destructive: true,
    });
    if (!ok) return;
    setError("");
    try {
      await dbCommand(nodeId, serverId, "db.collection.edit", {
        id,
        op: "drop",
        collection: coll.name,
      });
      await changed();
      onDropped();
    } catch (err) {
      setError(message(err));
    }
  }

  const d = docs.data;
  const from = d && d.documents.length > 0 ? skip + 1 : 0;
  const to = skip + (d?.documents.length ?? 0);

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="font-mono text-sm">{coll.name}</h3>
        <span className="text-xs text-muted-foreground">
          about {coll.count.toLocaleString()} documents
          {coll.indexes.length > 0 &&
            ` · indexes ${coll.indexes.map((i) => `${i.name}${i.unique ? " (unique)" : ""}`).join(", ")}`}
        </span>
        {canWrite && (
          <div className="ml-auto flex gap-1">
            <Button size="sm" variant="outline" onClick={() => setEditing({})}>
              <Plus /> Insert
            </Button>
            <Button size="sm" variant="ghost" onClick={drop}>
              <Trash2 /> Drop
            </Button>
          </div>
        )}
      </div>
      <form onSubmit={run} className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            aria-label="Filter"
            className="pl-8 font-mono"
            placeholder='Filter, like {"name": "alex"} or {"kills": {"$gt": 10}}'
            spellCheck={false}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Input
          aria-label="Sort"
          className="font-mono sm:w-48"
          placeholder='Sort, like {"kills": -1}'
          spellCheck={false}
          value={sort}
          onChange={(e) => setSort(e.target.value)}
        />
        <Button type="submit">
          <Play /> Find
        </Button>
      </form>
      {docs.error && <p className="text-sm text-destructive">{message(docs.error)}</p>}
      {docs.isPending && <p className="text-sm text-muted-foreground">Reading documents…</p>}
      {d && (
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>
            {d.documents.length === 0
              ? "No documents match."
              : `${from.toLocaleString()}–${to.toLocaleString()} of ${d.count < 0 ? "many" : d.count.toLocaleString()} · ${d.millis} ms`}
            {d.cut ? " · a page holds 2 MB of documents at most" : ""}
          </span>
          <span className="flex items-center gap-1">
            {canWrite && selected.size > 0 && (
              <Button size="sm" variant="ghost" onClick={removeSelected}>
                <Trash2 /> Delete {selected.size}
              </Button>
            )}
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="The page before"
              disabled={skip === 0}
              onClick={() => setSkip(Math.max(0, skip - pageSize))}
            >
              <ChevronLeft />
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="The next page"
              disabled={!d.more}
              onClick={() => setSkip(skip + d.documents.length)}
            >
              <ChevronRight />
            </Button>
          </span>
        </div>
      )}
      <div className="flex flex-col gap-2">
        {d?.documents.map((doc) => {
          const key = docID(doc);
          return (
            <DocumentCard
              key={key}
              doc={doc}
              selected={selected.has(key)}
              canWrite={canWrite}
              onSelect={(on) => {
                const next = new Set(selected);
                if (on) next.add(key);
                else next.delete(key);
                setSelected(next);
              }}
              onEdit={() => setEditing({ doc })}
            />
          );
        })}
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      {editing && (
        <DocumentDialog
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          collection={coll.name}
          doc={editing.doc}
          onDone={async (saved) => {
            setEditing(undefined);
            if (saved) await changed();
          }}
        />
      )}
    </div>
  );
}

function DocumentCard({
  doc,
  selected,
  canWrite,
  onSelect,
  onEdit,
}: {
  doc: string;
  selected: boolean;
  canWrite: boolean;
  onSelect: (on: boolean) => void;
  onEdit: () => void;
}) {
  const [open, setOpen] = useState(false);
  const text = pretty(doc);
  const lines = text.split("\n");
  const long = lines.length > 12;
  return (
    <div className={cn("rounded-lg border bg-card", selected && "border-primary bg-brand-wash")}>
      <div className="flex items-start gap-2 p-3">
        {canWrite && (
          <input
            type="checkbox"
            aria-label="Select the document"
            className="mt-1 accent-primary"
            checked={selected}
            onChange={(e) => onSelect(e.target.checked)}
          />
        )}
        <pre className="min-w-0 flex-1 overflow-x-auto font-mono text-xs leading-relaxed">
          {long && !open ? `${lines.slice(0, 12).join("\n")}\n  …` : text}
        </pre>
        {canWrite && (
          <Button size="icon-sm" variant="ghost" aria-label="Edit the document" onClick={onEdit}>
            <Pencil />
          </Button>
        )}
      </div>
      {long && (
        <button
          type="button"
          className="w-full border-t px-3 py-1 text-left text-xs text-muted-foreground hover:text-foreground"
          onClick={() => setOpen(!open)}
        >
          {open ? "Show less" : `Show all ${lines.length} lines`}
        </button>
      )}
    </div>
  );
}

// DocumentDialog inserts a document, or replaces one (its _id stays).
function DocumentDialog({
  nodeId,
  serverId,
  id,
  collection,
  doc,
  onDone,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  collection: string;
  doc?: string;
  onDone: (saved: boolean) => void;
}) {
  const settings = useFileSettings();
  const start = doc ? pretty(doc) : '{\n  "name": ""\n}';
  const [text, setText] = useState(start);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save() {
    setBusy(true);
    setError("");
    try {
      JSON.parse(text);
    } catch (err) {
      setError(`It isn't JSON: ${(err as Error).message}`);
      setBusy(false);
      return;
    }
    try {
      await dbCommand(nodeId, serverId, "db.document.edit", {
        id,
        collection,
        op: doc ? "replace" : "insert",
        doc_id: doc ? docID(doc) : undefined,
        document: text,
      });
      onDone(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onDone(false)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{doc ? "Edit the document" : `Insert into ${collection}`}</DialogTitle>
          <DialogDescription>
            MongoDB's Extended JSON: {'{"$oid": "…"}'} for an ID, {'{"$date": "…"}'} for a date.
            {doc ? " Its _id stays as it is." : " Leave out _id to have one made."}
          </DialogDescription>
        </DialogHeader>
        <Card className="h-80 overflow-hidden py-0">
          <Suspense
            fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
          >
            <MonacoEditor
              value={start}
              language="json"
              readOnly={false}
              settings={settings}
              onChange={setText}
              onSave={() => void save()}
            />
          </Suspense>
        </Card>
        {error && <p className="font-mono text-sm whitespace-pre-wrap text-destructive">{error}</p>}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onDone(false)}>
            Cancel
          </Button>
          <Button type="button" disabled={busy} onClick={() => void save()}>
            {busy && <Loader2 className="animate-spin" />} {doc ? "Save" : "Insert"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
