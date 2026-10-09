import {
  ChevronRight,
  Download,
  FileArchive,
  FilePlus,
  FolderPlus,
  Keyboard,
  Maximize2,
  Minimize2,
  MoreHorizontal,
  PackageOpen,
  PanelLeftClose,
  PanelLeftOpen,
  Pencil,
  RefreshCw,
  Search,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import {
  type DragEvent,
  type FormEvent,
  type MouseEvent,
  type ReactNode,
  useRef,
  useState,
} from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";
import { type ArchiveFormat, setFileSettings, useFileSettings } from "@/lib/file-settings";
import {
  baseName,
  crumbs,
  formatSize,
  isArchive,
  join,
  maxEdit,
  parent,
  validName,
} from "@/lib/files";
import { downloadURL, uploadFile } from "@/lib/upload";
import { cn } from "@/lib/utils";
import { type Entry, kind, sorted, useFilesApi, useListing } from "./data";
import { EditorPane } from "./editor-pane";
import { EntryIcon } from "./icons";
import { SettingsMenu, ThemeMenu } from "./settings";
import { ShortcutsDialog } from "./shortcuts";
import { Tree } from "./tree";

type PendingUpload = {
  key: string;
  name: string;
  file: File;
  progress: number;
  done?: boolean;
  error?: string;
  abort: AbortController;
};

// A name asked for in a dialog: a new file or folder, or a rename.
type NameAsk = { title: string; initial: string; action: string; done: (name: string) => void };

// Files is the server's file manager (docs/WINGS.md#files-and-sftp): a
// folder tree, the folder's contents, and Monaco for the open file. Every
// action is a command to the node, checked by the Panel and Wings, with the
// egg's denylist enforced by Wings.
export function Files({
  nodeId,
  serverId,
  canWrite,
  path,
  file,
  onNavigate,
  actions,
}: {
  nodeId: string;
  serverId: string;
  canWrite: boolean;
  path: string;
  file?: string;
  onNavigate: (to: { path: string; file?: string }) => void;
  // More toolbar buttons (the SFTP one).
  actions?: ReactNode;
}) {
  const api = useFilesApi(nodeId, serverId);
  const settings = useFileSettings();
  const listing = useListing(api, path);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string>();
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [uploads, setUploads] = useState<PendingUpload[]>([]);
  const [dragging, setDragging] = useState(false);
  const [full, setFull] = useState(false);
  const [shortcuts, setShortcuts] = useState(false);
  const [ask, setAsk] = useState<NameAsk>();
  const [confirmDelete, setConfirmDelete] = useState<string[]>();
  const [compressing, setCompressing] = useState<string[]>();
  const dirty = useRef(false);
  const picker = useRef<HTMLInputElement>(null);

  const entries = sorted(listing.data?.entries ?? []).filter((e) =>
    e.name.toLowerCase().includes(query.trim().toLowerCase()),
  );

  function go(to: { path: string; file?: string }) {
    if (
      dirty.current &&
      to.file !== file &&
      !window.confirm("Leave without saving? Your changes stay in this browser as a draft.")
    )
      return;
    setSelected(new Set());
    setQuery("");
    setError("");
    onNavigate(to);
  }

  function openEntry(dir: string, e: Entry) {
    if (e.denied) return;
    const p = join(dir, e.name);
    if (kind(e) === "dir") go({ path: p });
    else if (kind(e) === "file") {
      if (e.size > maxEdit) {
        setError(`${e.name} is too large for the editor (over 4 MB). Download it instead.`);
        return;
      }
      go({ path: dir, file: p });
    }
  }

  async function act(fn: () => Promise<unknown>, done?: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await fn();
      if (done) setNotice(done);
      setSelected(new Set());
      await api.refresh();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  // Selection: Ctrl/Cmd toggles, Shift selects a range from the last one.
  function select(e: Entry, ev: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean }) {
    const next = new Set(selected);
    if (ev.shiftKey && anchor) {
      const names = entries.map((x) => x.name);
      const i = names.indexOf(anchor);
      const j = names.indexOf(e.name);
      for (const n of names.slice(Math.min(i, j), Math.max(i, j) + 1)) next.add(n);
    } else if (next.has(e.name)) next.delete(e.name);
    else next.add(e.name);
    setSelected(next);
    setAnchor(e.name);
  }

  function rowClick(e: Entry, ev: MouseEvent) {
    if (ev.shiftKey || ev.metaKey || ev.ctrlKey || !settings.singleClick) {
      if (canWrite) select(e, ev);
      return;
    }
    openEntry(path, e);
  }

  // Uploads go one after another, each in chunks that resume.
  function startUploads(list: FileList | File[] | null) {
    if (!list || list.length === 0 || !canWrite) return;
    const dir = path;
    const added: PendingUpload[] = [...list].map((f) => ({
      key: `${Date.now()}-${f.name}-${Math.random()}`,
      name: f.name,
      file: f,
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
          void api.refresh();
        } catch (err) {
          if (!u.abort.signal.aborted) update(u.key, { error: message(err) });
        }
      }
    })();
  }
  function update(key: string, change: Partial<PendingUpload>) {
    setUploads((list) => list.map((u) => (u.key === key ? { ...u, ...change } : u)));
  }

  const newFile = () =>
    setAsk({
      title: "New file",
      initial: "",
      action: "Create",
      done: (name) =>
        void act(async () => {
          await api.run("files.write", { path: join(path, name), data: "" });
          go({ path, file: join(path, name) });
        }),
    });
  const newFolder = () =>
    setAsk({
      title: "New folder",
      initial: "",
      action: "Create",
      done: (name) => void act(() => api.run("files.mkdir", { path: join(path, name) })),
    });
  const rename = (e: Entry) =>
    setAsk({
      title: `Rename ${e.name}`,
      initial: e.name,
      action: "Rename",
      done: (name) =>
        void act(() => api.run("files.rename", { dir: path, moves: [{ from: e.name, to: name }] })),
    });
  const compress = (names: string[]) => setCompressing(names);
  const decompress = (e: Entry) =>
    void act(
      () => api.run("files.decompress", { path: join(path, e.name), dest: path }),
      `Extracting ${e.name}. Its files appear here when it's done.`,
    );

  function onDrop(ev: DragEvent) {
    ev.preventDefault();
    setDragging(false);
    startUploads(ev.dataTransfer.files);
  }

  const allSelected = entries.length > 0 && entries.every((e) => selected.has(e.name));

  return (
    <div
      className={cn(
        "flex flex-col overflow-hidden rounded-xl border bg-card",
        full ? "fixed inset-0 z-50 rounded-none border-0" : "h-[calc(100svh-15rem)] min-h-[32rem]",
      )}
    >
      {/* The toolbar: where you are, what you can make, and the settings. */}
      <div className="flex min-h-14 shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <nav aria-label="Folder" className="flex min-w-0 flex-1 items-center gap-1 text-sm">
          <button
            type="button"
            className="rounded px-1 font-medium hover:bg-muted"
            onClick={() => go({ path: "" })}
          >
            Root
          </button>
          {crumbs(path).map((c) => (
            <span key={c.path} className="flex min-w-0 items-center gap-1">
              <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
              <button
                type="button"
                className="truncate rounded px-1 font-medium hover:bg-muted"
                onClick={() => go({ path: c.path })}
              >
                {c.name}
              </button>
            </span>
          ))}
          {file && (
            <span className="flex min-w-0 items-center gap-1">
              <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate px-1 font-medium">{baseName(file)}</span>
            </span>
          )}
        </nav>
        <div className="flex flex-wrap items-center gap-1.5">
          {canWrite && (
            <>
              <Button size="sm" variant="outline" disabled={busy} onClick={newFile}>
                <FilePlus /> New File
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={newFolder}>
                <FolderPlus /> New Folder
              </Button>
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
            </>
          )}
          {actions}
          <div className="relative w-44 lg:w-56">
            <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
            <Input
              type="search"
              aria-label="Search this folder"
              placeholder="Search files…"
              className="h-8 pl-8"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                if (file) go({ path });
              }}
            />
          </div>
          <SettingsMenu />
          <ThemeMenu />
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Keyboard shortcuts"
            title="Keyboard shortcuts"
            onClick={() => setShortcuts(true)}
          >
            <Keyboard />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={full ? "Leave full screen" : "Full screen"}
            title={full ? "Leave full screen" : "Full screen"}
            onClick={() => setFull(!full)}
          >
            {full ? <Minimize2 /> : <Maximize2 />}
          </Button>
        </div>
      </div>
      <p className="shrink-0 border-b px-4 py-1.5 text-xs text-muted-foreground">
        {settings.singleClick
          ? "Single-click to open · tick the checkbox to select · Ctrl or Shift-click also selects"
          : "Double-click to open · click, Ctrl or Shift-click to select"}
        {canWrite && " · drop files anywhere to upload"}
      </p>

      <div className="flex min-h-0 flex-1">
        {/* The tree. */}
        {settings.treeOpen ? (
          <aside className="hidden w-72 shrink-0 flex-col border-r md:flex">
            <div className="flex h-10 shrink-0 items-center justify-between px-3">
              <span className="font-mono text-xs tracking-wide text-muted-foreground uppercase">
                Files
              </span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label="Hide the file tree"
                onClick={() => setFileSettings({ treeOpen: false })}
              >
                <PanelLeftClose />
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto px-1.5 pb-2">
              <Tree
                api={api}
                current={path}
                openFile={file}
                onFolder={(p) => go({ path: p })}
                onFile={(p, e) => openEntry(parent(p), e)}
              />
            </div>
          </aside>
        ) : (
          <div className="hidden shrink-0 border-r p-1.5 md:block">
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label="Show the file tree"
              onClick={() => setFileSettings({ treeOpen: true })}
            >
              <PanelLeftOpen />
            </Button>
          </div>
        )}

        {/* The folder or the open file. */}
        <main
          className="relative flex min-w-0 flex-1 flex-col"
          onDragOver={(e) => {
            if (!canWrite || file) return;
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={(e) => {
            if (e.currentTarget === e.target) setDragging(false);
          }}
          onDrop={onDrop}
        >
          {file ? (
            <EditorPane
              key={file}
              api={api}
              nodeId={nodeId}
              serverId={serverId}
              path={file}
              canWrite={canWrite}
              onBack={() => go({ path })}
              onClose={() => go({ path })}
              onDirty={(d) => {
                dirty.current = d;
              }}
            />
          ) : (
            <>
              {(error || notice || uploads.length > 0 || selected.size > 0) && (
                <div className="flex shrink-0 flex-col gap-2 border-b px-4 py-2">
                  {error && <p className="text-sm text-destructive">{error}</p>}
                  {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
                  {selected.size > 0 && canWrite && (
                    <div className="flex flex-wrap items-center gap-2 text-sm">
                      <span className="font-medium">{selected.size} selected</span>
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
                        className="text-destructive"
                        disabled={busy}
                        onClick={() => setConfirmDelete([...selected])}
                      >
                        <Trash2 /> Delete
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
                        Clear
                      </Button>
                    </div>
                  )}
                  {uploads.length > 0 && (
                    <ul className="flex flex-col gap-1 text-sm">
                      {uploads.map((u) => (
                        <li key={u.key} className="flex items-center gap-3">
                          <Upload className="size-3.5 shrink-0 text-muted-foreground" />
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
                            onClick={() => {
                              u.abort.abort();
                              setUploads((l) => l.filter((x) => x.key !== u.key));
                            }}
                          >
                            <X />
                          </Button>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
              <div className="min-h-0 flex-1 overflow-auto p-4">
                <div className="overflow-hidden rounded-lg border">
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs text-muted-foreground">
                      <tr className="border-b">
                        {canWrite && (
                          <th className="w-10 px-3 py-2.5">
                            <Checkbox
                              aria-label="Select everything here"
                              checked={allSelected}
                              onCheckedChange={(v) =>
                                setSelected(v ? new Set(entries.map((e) => e.name)) : new Set())
                              }
                            />
                          </th>
                        )}
                        <th className="px-3 py-2.5 font-medium">Name</th>
                        <th className="w-28 px-3 py-2.5 text-right font-medium">Size</th>
                        <th className="hidden w-52 px-3 py-2.5 font-medium lg:table-cell">
                          Modified
                        </th>
                        <th className="w-12 px-2 py-2.5" />
                      </tr>
                    </thead>
                    <tbody className="divide-y">
                      {listing.isPending && (
                        <tr>
                          <td className="px-3 py-3 text-muted-foreground" colSpan={5}>
                            Loading…
                          </td>
                        </tr>
                      )}
                      {listing.error && (
                        <tr>
                          <td className="px-3 py-3 text-destructive" colSpan={5}>
                            {message(listing.error)}
                          </td>
                        </tr>
                      )}
                      {listing.data && entries.length === 0 && (
                        <tr>
                          <td className="px-3 py-6 text-center text-muted-foreground" colSpan={5}>
                            {query ? `Nothing here matches "${query}".` : "This folder is empty."}
                          </td>
                        </tr>
                      )}
                      {entries.map((e) => (
                        <tr
                          key={e.name}
                          className={cn(
                            "cursor-default select-none",
                            e.denied ? "opacity-50" : "hover:bg-muted/50",
                            selected.has(e.name) && "bg-muted",
                          )}
                          onClick={(ev) => rowClick(e, ev)}
                          onDoubleClick={() => !settings.singleClick && openEntry(path, e)}
                        >
                          {canWrite && (
                            // biome-ignore lint/a11y/useKeyWithClickEvents: the checkbox inside takes the keyboard
                            <td className="px-3 py-2.5" onClick={(ev) => ev.stopPropagation()}>
                              <Checkbox
                                aria-label={`Select ${e.name}`}
                                disabled={e.denied}
                                checked={selected.has(e.name)}
                                onCheckedChange={() =>
                                  select(e, { shiftKey: false, metaKey: false, ctrlKey: false })
                                }
                              />
                            </td>
                          )}
                          <td className="px-3 py-2.5">
                            <span
                              className="flex items-center gap-2.5"
                              title={
                                e.denied ? "The game's egg doesn't allow opening this" : e.target
                              }
                            >
                              <EntryIcon e={e} />
                              <span className="break-all">{e.name}</span>
                            </span>
                          </td>
                          <td className="px-3 py-2.5 text-right text-muted-foreground tabular-nums">
                            {kind(e) === "file" ? formatSize(e.size) : "—"}
                          </td>
                          <td className="hidden px-3 py-2.5 text-muted-foreground lg:table-cell">
                            {new Date(e.modified_at).toLocaleString(undefined, {
                              dateStyle: "medium",
                              timeStyle: "short",
                            })}
                          </td>
                          {/* biome-ignore lint/a11y/useKeyWithClickEvents: the menu button inside takes the keyboard */}
                          <td
                            className="px-2 py-1 text-right"
                            onClick={(ev) => ev.stopPropagation()}
                          >
                            {!e.denied && (
                              <RowMenu
                                e={e}
                                canWrite={canWrite}
                                download={
                                  kind(e) === "file"
                                    ? downloadURL(nodeId, serverId, join(path, e.name))
                                    : undefined
                                }
                                onOpen={() => openEntry(path, e)}
                                onRename={() => rename(e)}
                                onExtract={() => decompress(e)}
                                onCompress={() => compress([e.name])}
                                onDelete={() => setConfirmDelete([e.name])}
                              />
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {listing.data?.truncated && (
                  <p className="mt-2 text-xs text-muted-foreground">
                    Only the first 10,000 entries are shown. Use SFTP for folders this large.
                  </p>
                )}
                <div className="mt-2 flex justify-end">
                  <Button size="xs" variant="ghost" onClick={() => void api.refresh()}>
                    <RefreshCw /> Refresh
                  </Button>
                </div>
              </div>
              {dragging && (
                <div className="pointer-events-none absolute inset-2 flex items-center justify-center rounded-lg border-2 border-dashed border-primary bg-background/80 text-sm font-medium">
                  Drop to upload to /{path}
                </div>
              )}
            </>
          )}
        </main>
      </div>

      <ShortcutsDialog open={shortcuts} onOpenChange={setShortcuts} />
      {compressing && (
        <CompressDialog
          names={compressing}
          onClose={() => setCompressing(undefined)}
          onCompress={(format, name) => {
            const names = compressing;
            setCompressing(undefined);
            void act(
              () => api.run("files.compress", { dir: path, names, format, name }),
              `Compressing into ${name}.${format}. It appears here when it's done.`,
            );
          }}
        />
      )}
      {ask && <NameDialog ask={ask} onClose={() => setAsk(undefined)} />}
      <Dialog open={!!confirmDelete} onOpenChange={(o) => !o && setConfirmDelete(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete{" "}
              {confirmDelete?.length === 1
                ? `"${confirmDelete[0]}"`
                : `${confirmDelete?.length} items`}
              ?
            </DialogTitle>
            <DialogDescription>
              Folders are deleted with everything in them. This can't be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmDelete(undefined)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => {
                const names = confirmDelete ?? [];
                setConfirmDelete(undefined);
                void act(() => api.run("files.delete", { dir: path, names }));
              }}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function RowMenu({
  e,
  canWrite,
  download,
  onOpen,
  onRename,
  onExtract,
  onCompress,
  onDelete,
}: {
  e: Entry;
  canWrite: boolean;
  download?: string;
  onOpen: () => void;
  onRename: () => void;
  onExtract: () => void;
  onCompress: () => void;
  onDelete: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button size="icon-sm" variant="ghost" aria-label={`Actions for ${e.name}`} />}
      >
        <MoreHorizontal />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onClick={onOpen}>Open</DropdownMenuItem>
        {download && (
          <DropdownMenuItem onClick={() => window.location.assign(download)}>
            <Download /> Download
          </DropdownMenuItem>
        )}
        {canWrite && (
          <>
            <DropdownMenuItem onClick={onRename}>
              <Pencil /> Rename
            </DropdownMenuItem>
            {kind(e) === "file" && isArchive(e.name) ? (
              <DropdownMenuItem onClick={onExtract}>
                <PackageOpen /> Extract here
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onClick={onCompress}>
                <FileArchive /> Compress
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={onDelete}>
              <Trash2 /> Delete
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function NameDialog({ ask, onClose }: { ask: NameAsk; onClose: () => void }) {
  const [name, setName] = useState(ask.initial);
  const trimmed = name.trim();
  const ok = validName(trimmed) && trimmed !== ask.initial;
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!ok) return;
    onClose();
    ask.done(trimmed);
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{ask.title}</DialogTitle>
          </DialogHeader>
          <Input
            aria-label="Name"
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            onFocus={(e) => {
              // Select the name without its extension, as file managers do.
              const dot = e.target.value.lastIndexOf(".");
              e.target.setSelectionRange(0, dot > 0 ? dot : e.target.value.length);
            }}
          />
          {trimmed && !validName(trimmed) && (
            <p className="text-xs text-destructive">A name can't contain a slash, or be . or ..</p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ok}>
              {ask.action}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const archiveFormats: { value: ArchiveFormat; label: string; text: string }[] = [
  { value: "zip", label: "ZIP", text: "Opens anywhere: Windows, macOS, and Linux." },
  { value: "tar.gz", label: "TAR.GZ", text: "The Linux standard. Smaller than ZIP." },
  { value: "tar.zst", label: "TAR.ZST", text: "Smallest and fastest. Needs newer tools to open." },
  { value: "tar", label: "TAR", text: "No compression: fastest, and the biggest." },
];

// CompressDialog asks what to call the archive and which format it's in.
function CompressDialog({
  names,
  onClose,
  onCompress,
}: {
  names: string[];
  onClose: () => void;
  onCompress: (format: ArchiveFormat, name: string) => void;
}) {
  const settings = useFileSettings();
  const [format, setFormat] = useState<ArchiveFormat>(settings.archiveFormat);
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const [name, setName] = useState(
    names.length === 1 && names[0]
      ? names[0].replace(/\.[^.]*$/, "")
      : `archive-${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}_${pad(now.getHours())}-${pad(now.getMinutes())}`,
  );
  const trimmed = name.trim();
  const ok = validName(trimmed);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!ok) return;
    setFileSettings({ archiveFormat: format });
    onCompress(format, trimmed);
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              Compress {names.length === 1 ? `"${names[0]}"` : `${names.length} items`}
            </DialogTitle>
            <DialogDescription>The archive is made in this folder.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="archive-name">Name</Label>
            <div className="flex items-center gap-2">
              <Input
                id="archive-name"
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <span className="shrink-0 font-mono text-sm text-muted-foreground">.{format}</span>
            </div>
            {trimmed && !ok && (
              <p className="text-xs text-destructive">
                A name can't contain a slash, or be . or ..
              </p>
            )}
          </div>
          <fieldset className="grid gap-2">
            <legend className="mb-2 text-sm font-medium">Format</legend>
            <div className="grid grid-cols-2 gap-2">
              {archiveFormats.map((f) => (
                <button
                  key={f.value}
                  type="button"
                  aria-pressed={format === f.value}
                  onClick={() => setFormat(f.value)}
                  className={cn(
                    "flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50",
                    format === f.value && "border-primary bg-muted",
                  )}
                >
                  <span className="font-mono text-sm font-medium">{f.label}</span>
                  <span className="text-xs text-muted-foreground">{f.text}</span>
                </button>
              ))}
            </div>
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ok}>
              <FileArchive /> Compress
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
