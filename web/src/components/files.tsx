import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Download,
  File,
  FileArchive,
  Folder,
  FolderPlus,
  Link2,
  Lock,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { lazy, Suspense, useMemo, useRef, useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { message } from "@/lib/errors";
import {
  baseName,
  crumbs,
  formatSize,
  fromBase64,
  isArchive,
  join,
  languageFor,
  looksBinary,
  maxEdit,
  validName,
} from "@/lib/files";
import { base64 } from "@/lib/servers";
import { commandClient } from "@/lib/transport";
import { downloadURL, uploadFile } from "@/lib/upload";

const Editor = lazy(() => import("@/components/editor"));

// What Wings' files actions return (internal/wings/files).
type Entry = {
  name: string;
  type: "file" | "dir" | "symlink" | "other";
  size: number;
  mode: number;
  modified_at: number;
  target?: string;
  target_type?: string;
  denied?: boolean;
};
type Listing = { path: string; entries: Entry[]; truncated?: boolean };
type Content = Entry & { data: string };
type PendingUpload = {
  key: string;
  name: string;
  file: File;
  progress: number;
  done?: boolean;
  error?: string;
  abort: AbortController;
};

// Files is the web file manager (docs/WINGS.md#files-and-sftp): every
// action is a command to the node, checked by the Panel and Wings, with the
// egg's denylist enforced by Wings.
export function Files({
  nodeId,
  serverId,
  canWrite,
  path,
  onPath,
}: {
  nodeId: string;
  serverId: string;
  canWrite: boolean;
  path: string;
  onPath: (path: string) => void;
}) {
  const client = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<string>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [uploads, setUploads] = useState<PendingUpload[]>([]);
  const picker = useRef<HTMLInputElement>(null);

  async function run<T>(action: string, params: object): Promise<T | undefined> {
    const res = await commandClient.execute({
      nodeId,
      action,
      serverId,
      paramsJson: JSON.stringify(params),
    });
    return res.resultJson ? (JSON.parse(res.resultJson) as T) : undefined;
  }

  const listing = useQuery({
    queryKey: ["files", nodeId, serverId, path],
    queryFn: () => run<Listing>("files.list", { path }),
  });
  const refresh = () => client.invalidateQueries({ queryKey: ["files", nodeId, serverId] });

  async function act(fn: () => Promise<unknown>, done?: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await fn();
      if (done) setNotice(done);
      setSelected(new Set());
      await refresh();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  function ask(question: string, initial = ""): string | undefined {
    const name = window.prompt(question, initial)?.trim();
    if (name === undefined || name === "" || name === initial) return undefined;
    if (!validName(name)) {
      setError("A name can't be empty, contain a slash, or be . or ..");
      return undefined;
    }
    return name;
  }

  // Uploads go one after another, each in chunks that resume.
  function startUploads(list: FileList | null) {
    if (!list || list.length === 0) return;
    const dir = path;
    const added: PendingUpload[] = [...list].map((file) => ({
      key: `${Date.now()}-${file.name}-${Math.random()}`,
      name: file.name,
      file,
      progress: 0,
      abort: new AbortController(),
    }));
    setUploads((u) => [...u, ...added]);
    void (async () => {
      for (const u of added) {
        if (u.abort.signal.aborted) continue;
        try {
          await uploadFile({
            nodeId,
            serverId,
            dir,
            file: u.file,
            signal: u.abort.signal,
            onProgress: (f) => update(u.key, { progress: f }),
          });
          update(u.key, { progress: 1, done: true });
          void refresh();
        } catch (err) {
          if (!u.abort.signal.aborted) update(u.key, { error: message(err) });
        }
      }
    })();
  }
  function update(key: string, change: Partial<PendingUpload>) {
    setUploads((list) => list.map((u) => (u.key === key ? { ...u, ...change } : u)));
  }
  function dismiss(u: PendingUpload) {
    u.abort.abort();
    setUploads((list) => list.filter((x) => x.key !== u.key));
  }

  const newFolder = () => {
    const name = ask("New folder's name");
    if (name) void act(() => run("files.mkdir", { path: join(path, name) }));
  };
  const newFile = () => {
    const name = ask("New file's name");
    if (!name) return;
    void act(async () => {
      await run("files.write", { path: join(path, name), data: "" });
      setEditing(join(path, name));
    });
  };
  const rename = (e: Entry) => {
    const name = ask(`Rename ${e.name} to`, e.name);
    if (name)
      void act(() => run("files.rename", { dir: path, moves: [{ from: e.name, to: name }] }));
  };
  const remove = (names: string[]) => {
    const what = names.length === 1 ? `"${names[0]}"` : `${names.length} items`;
    if (!window.confirm(`Delete ${what}? This can't be undone.`)) return;
    void act(() => run("files.delete", { dir: path, names }));
  };
  const compress = (names: string[]) =>
    void act(
      () => run("files.compress", { dir: path, names }),
      "Compressing. The archive appears here when it's done.",
    );
  const decompress = (e: Entry) =>
    void act(
      () => run("files.decompress", { path: join(path, e.name), dest: path }),
      `Extracting ${e.name}. Its files appear here when it's done.`,
    );

  if (editing) {
    return (
      <FileEditor
        path={editing}
        canWrite={canWrite}
        read={() => run<Content>("files.read", { path: editing })}
        write={(data) => run("files.write", { path: editing, data: base64(data) })}
        onClose={() => {
          setEditing(undefined);
          void refresh();
        }}
      />
    );
  }

  const entries = listing.data?.entries ?? [];
  const toggle = (name: string) => {
    const next = new Set(selected);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setSelected(next);
  };
  const open = (e: Entry) => {
    const kind = e.type === "symlink" ? e.target_type : e.type;
    if (e.denied) return;
    if (kind === "dir") {
      setSelected(new Set());
      onPath(join(path, e.name));
    } else if (kind === "file") {
      if (e.size > maxEdit) setError(`${e.name} is too large for the editor (over 4 MB).`);
      else setEditing(join(path, e.name));
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <nav className="flex flex-wrap items-center gap-1 text-sm" aria-label="Folder">
          <button type="button" className="hover:underline" onClick={() => onPath("")}>
            Server
          </button>
          {crumbs(path).map((c) => (
            <span key={c.path} className="flex items-center gap-1">
              <span className="text-muted-foreground">/</span>
              <button type="button" className="hover:underline" onClick={() => onPath(c.path)}>
                {c.name}
              </button>
            </span>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-1">
          {canWrite && selected.size > 0 && (
            <>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => compress([...selected])}
              >
                <FileArchive /> Compress
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => remove([...selected])}
              >
                <Trash2 /> Delete
              </Button>
            </>
          )}
          {canWrite && (
            <>
              <Button size="sm" variant="outline" onClick={() => picker.current?.click()}>
                <Upload /> Upload
              </Button>
              <input
                ref={picker}
                type="file"
                multiple
                hidden
                onChange={(e) => {
                  startUploads(e.target.files);
                  e.target.value = "";
                }}
              />
              <Button size="sm" variant="outline" disabled={busy} onClick={newFile}>
                <Plus /> File
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={newFolder}>
                <FolderPlus /> Folder
              </Button>
            </>
          )}
          <Button size="sm" variant="ghost" aria-label="Refresh" onClick={() => void refresh()}>
            <RefreshCw />
          </Button>
        </div>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
      {uploads.length > 0 && (
        <ul className="flex flex-col gap-1.5 rounded-lg border p-3 text-sm">
          {uploads.map((u) => (
            <li key={u.key} className="flex items-center gap-3">
              <span className="min-w-0 flex-1 truncate">{u.name}</span>
              {u.error ? (
                <span className="text-xs text-destructive">{u.error}</span>
              ) : u.done ? (
                <span className="text-xs text-muted-foreground">Uploaded</span>
              ) : (
                <span className="flex w-40 items-center gap-2">
                  <span className="h-1.5 flex-1 overflow-hidden rounded bg-muted">
                    <span
                      className="block h-full bg-primary transition-[width]"
                      style={{ width: `${Math.round(u.progress * 100)}%` }}
                    />
                  </span>
                  <span className="w-9 text-right text-xs tabular-nums text-muted-foreground">
                    {Math.round(u.progress * 100)}%
                  </span>
                </span>
              )}
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={u.done || u.error ? `Hide ${u.name}` : `Cancel ${u.name}`}
                onClick={() => dismiss(u)}
              >
                <X />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {listing.error && <p className="text-sm text-destructive">{message(listing.error)}</p>}
      <div className="overflow-hidden rounded-lg border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              {canWrite && <th className="w-8 px-3 py-2" />}
              <th className="px-3 py-2 font-medium">Name</th>
              <th className="w-24 px-3 py-2 text-right font-medium">Size</th>
              <th className="hidden w-44 px-3 py-2 font-medium sm:table-cell">Modified</th>
              <th className="w-32 px-3 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y">
            {path && (
              <tr className="hover:bg-muted/30">
                {canWrite && <td />}
                <td className="px-3 py-2" colSpan={canWrite ? 4 : 3}>
                  <button
                    type="button"
                    className="flex items-center gap-2 hover:underline"
                    onClick={() => onPath(path.split("/").slice(0, -1).join("/"))}
                  >
                    <ArrowLeft className="size-4" /> Up
                  </button>
                </td>
              </tr>
            )}
            {listing.isPending && (
              <tr>
                <td className="px-3 py-3 text-muted-foreground" colSpan={5}>
                  Loading…
                </td>
              </tr>
            )}
            {listing.data && entries.length === 0 && (
              <tr>
                <td className="px-3 py-3 text-muted-foreground" colSpan={5}>
                  This folder is empty.
                </td>
              </tr>
            )}
            {entries.map((e) => (
              <tr key={e.name} className={e.denied ? "opacity-50" : "hover:bg-muted/30"}>
                {canWrite && (
                  <td className="px-3 py-2">
                    <input
                      type="checkbox"
                      aria-label={`Select ${e.name}`}
                      disabled={e.denied}
                      checked={selected.has(e.name)}
                      onChange={() => toggle(e.name)}
                    />
                  </td>
                )}
                <td className="px-3 py-2">
                  <button
                    type="button"
                    disabled={e.denied}
                    title={e.denied ? "The game's egg doesn't allow opening this" : e.target}
                    className="flex items-center gap-2 text-left hover:underline disabled:no-underline"
                    onClick={() => open(e)}
                  >
                    <EntryIcon e={e} />
                    <span className="break-all">{e.name}</span>
                  </button>
                </td>
                <td className="px-3 py-2 text-right text-muted-foreground tabular-nums">
                  {e.type === "file" ? formatSize(e.size) : ""}
                </td>
                <td className="hidden px-3 py-2 text-muted-foreground sm:table-cell">
                  {new Date(e.modified_at).toLocaleString()}
                </td>
                <td className="px-3 py-1 text-right">
                  {!e.denied && (
                    <div className="flex justify-end gap-0.5">
                      {e.type === "file" && (
                        <a
                          href={downloadURL(nodeId, serverId, join(path, e.name))}
                          aria-label={`Download ${e.name}`}
                          className={buttonVariants({ size: "icon-sm", variant: "ghost" })}
                        >
                          <Download />
                        </a>
                      )}
                      {canWrite && (
                        <>
                          {e.type === "file" && isArchive(e.name) && (
                            <Button
                              size="icon-sm"
                              variant="ghost"
                              aria-label={`Extract ${e.name}`}
                              disabled={busy}
                              onClick={() => decompress(e)}
                            >
                              <FileArchive />
                            </Button>
                          )}
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Rename ${e.name}`}
                            disabled={busy}
                            onClick={() => rename(e)}
                          >
                            <Pencil />
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Delete ${e.name}`}
                            disabled={busy}
                            onClick={() => remove([e.name])}
                          >
                            <Trash2 />
                          </Button>
                        </>
                      )}
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {listing.data?.truncated && (
        <p className="text-xs text-muted-foreground">
          Only the first 10,000 entries are shown. Use SFTP for folders this large.
        </p>
      )}
    </div>
  );
}

function EntryIcon({ e }: { e: Entry }) {
  const cls = "size-4 shrink-0 text-muted-foreground";
  if (e.denied) return <Lock className={cls} />;
  if (e.type === "symlink") return <Link2 className={cls} />;
  if (e.type === "dir") return <Folder className={cls} />;
  if (isArchive(e.name)) return <FileArchive className={cls} />;
  return <File className={cls} />;
}

function FileEditor({
  path,
  canWrite,
  read,
  write,
  onClose,
}: {
  path: string;
  canWrite: boolean;
  read: () => Promise<Content | undefined>;
  write: (data: Uint8Array) => Promise<unknown>;
  onClose: () => void;
}) {
  // Read once: saving doesn't reload the file, which would reset the
  // editor and its cursor.
  const file = useQuery({
    queryKey: ["file", path],
    queryFn: read,
    gcTime: 0,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  const [text, setText] = useState<string>();
  const [base, setBase] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const loaded = useMemo(() => {
    const data = file.data?.data ? fromBase64(file.data.data) : new Uint8Array();
    return { binary: looksBinary(data), text: new TextDecoder().decode(data) };
  }, [file.data]);
  const binary = file.data !== undefined && loaded.binary;
  const dirty = text !== undefined && text !== (base ?? loaded.text);

  async function save() {
    if (!canWrite || text === undefined) return;
    setSaving(true);
    setError("");
    try {
      await write(new TextEncoder().encode(text));
      setBase(text);
      setSaved(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setSaving(false);
    }
  }

  function close() {
    if (dirty && !window.confirm("Discard your changes?")) return;
    onClose();
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={close}>
          <ArrowLeft /> Files
        </Button>
        <span className="truncate font-mono text-sm">{path}</span>
        {dirty && <span className="text-xs text-muted-foreground">Unsaved</span>}
        {!dirty && saved && <span className="text-xs text-muted-foreground">Saved</span>}
        {canWrite && !binary && (
          <Button size="sm" className="ml-auto" disabled={!dirty || saving} onClick={save}>
            <Save /> {saving ? "Saving…" : "Save"}
          </Button>
        )}
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      {file.error && <p className="text-sm text-destructive">{message(file.error)}</p>}
      {file.isPending && <p className="text-sm text-muted-foreground">Opening {baseName(path)}…</p>}
      {binary && (
        <p className="text-sm text-muted-foreground">
          This isn't a text file, so it can't be edited here.
        </p>
      )}
      {file.data && !binary && (
        <Suspense fallback={<p className="text-sm text-muted-foreground">Loading the editor…</p>}>
          <Editor
            initial={loaded.text}
            lang={languageFor(path)}
            readOnly={!canWrite}
            onChange={(t) => {
              setText(t);
              setSaved(false);
            }}
            onSave={() => void save()}
          />
        </Suspense>
      )}
    </div>
  );
}
