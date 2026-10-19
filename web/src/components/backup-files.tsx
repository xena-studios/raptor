import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import {
  ArchiveRestore,
  ArrowLeft,
  CheckCircle2,
  ChevronRight,
  Copy,
  FolderOpen,
  Loader2,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  RotateCcw,
  Search,
  X,
  XCircle,
} from "lucide-react";
import { useState } from "react";

import {
  type Entry,
  kind,
  sorted,
  useBackupFilesApi,
  useListing,
} from "@/components/file-manager/data";
import { EditorPane } from "@/components/file-manager/editor-pane";
import { EntryIcon } from "@/components/file-manager/icons";
import type { MenuTarget } from "@/components/file-manager/index";
import { Tree } from "@/components/file-manager/tree";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "@/components/ui/context-menu";
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
import { Input } from "@/components/ui/input";
import type { Backup } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { setFileSettings, useFileSettings } from "@/lib/file-settings";
import { baseName, crumbs, formatSize, join, parent } from "@/lib/files";
import { when } from "@/lib/format";
import { passkeyCancelled } from "@/lib/webauthn";

// A restore of some files from this backup, as backup.activity reports it.
type Pull = {
  job_id: string;
  status: "queued" | "running" | "ok" | "failed";
  folder?: string;
  paths?: number;
  error?: string;
};

// BackupFiles is a backup opened like the file manager, read-only: its
// folders in a tree and a list, and its files in the editor to look at.
// A file or folder is restored into a new .restore folder, leaving the
// server's own alone; Restore everything puts the whole server back as it
// was, signed by the user's passkey.
export function BackupFiles({
  orgId,
  nodeId,
  serverId,
  backup,
  path,
  file,
  pulls,
  onNavigate,
  onClose,
  onPull,
  onRestoreAll,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  backup: Backup;
  path: string;
  file?: string;
  // Restores from this backup started here, newest first.
  pulls: Pull[];
  onNavigate: (to: { path: string; file?: string }) => void;
  onClose: () => void;
  onPull: (paths: string[]) => Promise<void>;
  onRestoreAll: () => Promise<void>;
}) {
  const api = useBackupFilesApi(nodeId, serverId, backup.id);
  const settings = useFileSettings();
  const listing = useListing(api, path, !file);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string>();
  const [menuFor, setMenuFor] = useState<MenuTarget>();
  const [menuOpen, setMenuOpen] = useState(false);
  const [confirmAll, setConfirmAll] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const taken = when(backup.createdAt);

  const entries = sorted(listing.data?.entries ?? []).filter((e) =>
    e.name.toLowerCase().includes(query.trim().toLowerCase()),
  );

  function go(to: { path: string; file?: string }) {
    setSelected(new Set());
    setAnchor(undefined);
    onNavigate(to);
  }

  function open(dir: string, e: Entry) {
    if (e.denied) return;
    if (kind(e) === "dir") go({ path: join(dir, e.name) });
    else if (kind(e) === "file") go({ path: dir, file: join(dir, e.name) });
  }

  function rowClick(e: Entry, ev: React.MouseEvent) {
    if (e.denied) return;
    if (settings.singleClick && !(ev.metaKey || ev.ctrlKey || ev.shiftKey)) {
      open(path, e);
      return;
    }
    const next = new Set(ev.metaKey || ev.ctrlKey || ev.shiftKey ? selected : []);
    if (ev.shiftKey && anchor) {
      const names = entries.filter((x) => !x.denied).map((x) => x.name);
      const [a = 0, b = 0] = [names.indexOf(anchor), names.indexOf(e.name)].sort((x, y) => x - y);
      for (const n of names.slice(a, b + 1)) next.add(n);
    } else if (next.has(e.name) && (ev.metaKey || ev.ctrlKey)) {
      next.delete(e.name);
    } else {
      next.add(e.name);
      setAnchor(e.name);
    }
    setSelected(next);
  }

  function toggle(name: string) {
    const next = new Set(selected);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setSelected(next);
    setAnchor(name);
  }

  async function pull(paths: string[]) {
    setError("");
    try {
      await onPull(paths);
      setSelected(new Set());
    } catch (err) {
      setError(message(err));
    }
  }

  async function restoreAll() {
    setWorking(true);
    setError("");
    try {
      await onRestoreAll();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
      setWorking(false);
      setConfirmAll(false);
    }
  }

  const outlined = (name: string) =>
    menuOpen &&
    menuFor?.from === "list" &&
    menuFor.dir === path &&
    !!menuFor.items?.some((e) => e.name === name);
  const menuPaths = (t?: MenuTarget) =>
    t?.items ? t.items.map((e) => join(t.dir, e.name)) : t?.dir ? [t.dir] : [];
  const shown = pulls.filter((p) => !dismissed.has(p.job_id)).slice(0, 3);
  const allSelected =
    entries.some((e) => !e.denied) && entries.every((e) => e.denied || selected.has(e.name));

  return (
    <div className="flex h-[calc(100svh-15rem)] min-h-[32rem] flex-col overflow-hidden rounded-xl border bg-card">
      {/* Where you are in the backup, and restoring all of it. */}
      <div className="flex min-h-14 shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <Button size="icon-sm" variant="ghost" aria-label="Back to the backups" onClick={onClose}>
          <ArrowLeft />
        </Button>
        <nav
          aria-label="Folder in the backup"
          className="flex min-w-0 flex-1 items-center gap-1 text-sm"
        >
          <button
            type="button"
            className="shrink-0 rounded px-1 font-medium hover:bg-muted"
            onClick={() => go({ path: "" })}
          >
            Backup from {taken}
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
          <Button size="sm" variant="destructive" onClick={() => setConfirmAll(true)}>
            <RotateCcw /> Restore everything
          </Button>
        </div>
      </div>
      <p className="shrink-0 border-b px-4 py-1.5 text-xs text-muted-foreground">
        The server's files as they were on {taken}, read-only. Restoring a file or folder copies it
        into a new <code>.restore</code> folder and changes nothing else.
      </p>

      <div className="flex min-h-0 flex-1">
        {settings.treeOpen ? (
          <aside className="hidden w-72 shrink-0 flex-col border-r md:flex">
            <div className="flex h-10 shrink-0 items-center justify-between px-3">
              <span className="font-mono text-xs tracking-wide text-muted-foreground uppercase">
                Backup
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
              <ContextMenu onOpenChange={setMenuOpen}>
                <ContextMenuTrigger render={<div />}>
                  <Tree
                    api={api}
                    rootName="Server root"
                    current={path}
                    openFile={file}
                    menuFor={menuOpen ? menuFor : undefined}
                    onFolder={(p) => go({ path: p })}
                    onFile={(p, e) => open(parent(p), e)}
                    onMenu={(t) => setMenuFor({ ...t, from: "tree" })}
                  />
                </ContextMenuTrigger>
                <ContextMenuContent>
                  <PullMenu
                    target={menuFor}
                    onOpen={(dir, e) => open(dir, e)}
                    onPull={() => void pull(menuPaths(menuFor))}
                    onCopyPath={(p) => void navigator.clipboard.writeText(`/${p}`)}
                  />
                </ContextMenuContent>
              </ContextMenu>
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

        <main className="relative flex min-w-0 flex-1 flex-col">
          {(error || shown.length > 0) && (
            <div className="flex shrink-0 flex-col gap-1.5 border-b px-4 py-2">
              {error && <p className="text-sm text-destructive">{error}</p>}
              {shown.map((p) => (
                <PullNote
                  key={p.job_id}
                  p={p}
                  link={(folder) => (
                    <Link
                      to="/orgs/$orgId/servers/$serverId/files"
                      params={{ orgId, serverId }}
                      search={{ path: folder }}
                      className="inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline"
                    >
                      <FolderOpen className="size-3.5" /> Open in the file manager
                    </Link>
                  )}
                  onDismiss={() => setDismissed(new Set(dismissed).add(p.job_id))}
                />
              ))}
            </div>
          )}
          {file ? (
            <EditorPane
              key={file}
              api={api}
              nodeId={nodeId}
              serverId={serverId}
              path={file}
              canWrite={false}
              transfers={false}
              badge={
                <span className="rounded border px-1.5 text-xs text-muted-foreground">
                  From the backup
                </span>
              }
              actions={
                <Button size="sm" variant="outline" onClick={() => void pull([file])}>
                  <ArchiveRestore /> Restore to .restore
                </Button>
              }
              onBack={() => go({ path })}
              onClose={() => go({ path })}
              onDirty={() => {}}
            />
          ) : (
            <>
              {selected.size > 0 && (
                <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2 text-sm">
                  <span className="font-medium">{selected.size} selected</span>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => void pull([...selected].map((n) => join(path, n)))}
                  >
                    <ArchiveRestore /> Restore to .restore
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
                    Clear
                  </Button>
                </div>
              )}
              <ContextMenu onOpenChange={setMenuOpen}>
                <ContextMenuTrigger
                  render={<div />}
                  className="min-h-0 flex-1 overflow-auto p-4"
                  onContextMenu={(ev) => {
                    // Below the rows: the folder itself.
                    if (!(ev.target as HTMLElement).closest("tr[data-row]")) {
                      setMenuFor({ dir: path, items: null, from: "list" });
                    }
                  }}
                >
                  <div className="overflow-hidden rounded-lg border">
                    <table className="w-full text-sm">
                      <thead className="bg-muted/50 text-left text-muted-foreground">
                        <tr>
                          <th className="w-10 px-3 py-2.5">
                            <Checkbox
                              aria-label="Select everything here"
                              checked={allSelected}
                              onCheckedChange={(v) =>
                                setSelected(
                                  v
                                    ? new Set(entries.filter((e) => !e.denied).map((e) => e.name))
                                    : new Set(),
                                )
                              }
                            />
                          </th>
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
                              <span className="flex items-center gap-2">
                                <Loader2 className="size-4 animate-spin" /> Opening the backup…
                              </span>
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
                            data-row
                            className={cn(
                              "cursor-default select-none",
                              e.denied ? "opacity-50" : "hover:bg-muted/50",
                              selected.has(e.name) && "bg-muted",
                              outlined(e.name) && "outline-2 -outline-offset-2 outline-primary/60",
                            )}
                            onClick={(ev) => rowClick(e, ev)}
                            onDoubleClick={() => !settings.singleClick && open(path, e)}
                            onContextMenu={() => {
                              if (e.denied) return;
                              // Right-clicking a selected row is about the selection.
                              const items = selected.has(e.name)
                                ? entries.filter((x) => selected.has(x.name))
                                : [e];
                              setMenuFor({ dir: path, items, from: "list" });
                            }}
                          >
                            {/* biome-ignore lint/a11y/useKeyWithClickEvents: the checkbox inside takes the keyboard */}
                            <td className="px-3 py-2.5" onClick={(ev) => ev.stopPropagation()}>
                              <Checkbox
                                aria-label={`Select ${e.name}`}
                                disabled={e.denied}
                                checked={selected.has(e.name)}
                                onCheckedChange={() => toggle(e.name)}
                              />
                            </td>
                            <td className="px-3 py-2.5">
                              <span
                                className="flex items-center gap-2.5"
                                title={
                                  e.denied
                                    ? "The game's egg doesn't allow restoring this"
                                    : undefined
                                }
                              >
                                <EntryIcon e={e} />
                                <span className="break-all">{e.name}</span>
                              </span>
                            </td>
                            <td className="px-3 py-2.5 text-right text-muted-foreground tabular-nums">
                              {formatSize(e.size)}
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
                                <DropdownMenu>
                                  <DropdownMenuTrigger
                                    render={
                                      <Button
                                        size="icon-sm"
                                        variant="ghost"
                                        aria-label={`Actions for ${e.name}`}
                                      />
                                    }
                                  >
                                    <MoreHorizontal />
                                  </DropdownMenuTrigger>
                                  <DropdownMenuContent align="end" className="w-48">
                                    <PullMenu
                                      target={{ dir: path, items: [e] }}
                                      onOpen={(dir, x) => open(dir, x)}
                                      onPull={() => void pull([join(path, e.name)])}
                                      onCopyPath={(p) =>
                                        void navigator.clipboard.writeText(`/${p}`)
                                      }
                                    />
                                  </DropdownMenuContent>
                                </DropdownMenu>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </ContextMenuTrigger>
                <ContextMenuContent>
                  <PullMenu
                    target={menuFor}
                    onOpen={(dir, e) => open(dir, e)}
                    onPull={() => void pull(menuPaths(menuFor))}
                    onCopyPath={(p) => void navigator.clipboard.writeText(`/${p}`)}
                  />
                </ContextMenuContent>
              </ContextMenu>
            </>
          )}
        </main>
      </div>

      <Dialog open={confirmAll} onOpenChange={(o) => !o && !working && setConfirmAll(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Restore everything?</DialogTitle>
            <DialogDescription>
              The server is put back exactly as it was on {taken}.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 text-sm">
            <p>
              Every file is replaced with the backup's, and files made since are deleted. The server
              stops while it runs. A safety backup of what's there now is taken first, so this can
              be undone.
            </p>
            {backup.databases.length > 0 && (
              <p>
                Its {backup.databases.length === 1 ? "database" : "databases"} (
                {backup.databases.join(", ")}) go back too: each is emptied and filled from the
                backup.
              </p>
            )}
            <p className="text-muted-foreground">Your passkey signs it.</p>
          </div>
          <DialogFooter>
            <Button variant="outline" disabled={working} onClick={() => setConfirmAll(false)}>
              Cancel
            </Button>
            <Button variant="destructive" disabled={working} onClick={() => void restoreAll()}>
              {working ? <Loader2 className="animate-spin" /> : <RotateCcw />} Restore everything
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// PullMenu is a backup entry's menu, right-clicked or from its button:
// open it, restore it (or the folder, or what's selected) to .restore.
function PullMenu({
  target,
  onOpen,
  onPull,
  onCopyPath,
}: {
  target?: MenuTarget;
  onOpen: (dir: string, e: Entry) => void;
  onPull: () => void;
  onCopyPath: (path: string) => void;
}) {
  if (!target) return null;
  const items = target.items;
  if (items === null) {
    // The folder itself (none: the whole backup, which is Restore everything).
    return (
      <>
        {target.dir && (
          <DropdownMenuItem onClick={onPull}>
            <ArchiveRestore /> Restore this folder to .restore
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onClick={() => onCopyPath(target.dir)}>
          <Copy /> Copy path
        </DropdownMenuItem>
      </>
    );
  }
  const one = items.length === 1 ? items[0] : undefined;
  return (
    <>
      {one && (kind(one) === "dir" || kind(one) === "file") && (
        <DropdownMenuItem onClick={() => onOpen(target.dir, one)}>Open</DropdownMenuItem>
      )}
      <DropdownMenuItem onClick={onPull}>
        <ArchiveRestore /> Restore {one ? "" : `${items.length} items `}to .restore
      </DropdownMenuItem>
      {one && (
        <DropdownMenuItem onClick={() => onCopyPath(join(target.dir, one.name))}>
          <Copy /> Copy path
        </DropdownMenuItem>
      )}
    </>
  );
}

// PullNote is a restore to .restore started here: running, or done with a
// link to where it went.
function PullNote({
  p,
  link,
  onDismiss,
}: {
  p: Pull;
  link: (folder: string) => React.ReactNode;
  onDismiss: () => void;
}) {
  const items = `${p.paths ?? 0} ${p.paths === 1 ? "item" : "items"}`;
  const running = p.status === "queued" || p.status === "running";
  return (
    <div className="flex items-center gap-2 text-sm">
      {running ? (
        <Loader2 className="size-4 shrink-0 animate-spin text-primary" />
      ) : p.status === "ok" ? (
        <CheckCircle2 className="size-4 shrink-0 text-ok" />
      ) : (
        <XCircle className="size-4 shrink-0 text-destructive" />
      )}
      <span className="min-w-0 flex-1">
        {running
          ? `Restoring ${items} into ${p.folder ?? ".restore"}…`
          : p.status === "ok"
            ? `Restored ${items} into ${p.folder}.`
            : `Restoring ${items} failed${p.error ? `: ${p.error}` : "."}`}{" "}
        {p.status === "ok" && p.folder && link(p.folder)}
      </span>
      {!running && (
        <Button size="icon-xs" variant="ghost" aria-label="Dismiss" onClick={onDismiss}>
          <X />
        </Button>
      )}
    </div>
  );
}
