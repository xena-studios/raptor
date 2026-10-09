import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Archive,
  ArchiveRestore,
  CheckCircle2,
  ChevronRight,
  Clock,
  FileStack,
  Folder,
  FolderOpen,
  Loader2,
  Lock,
  LockOpen,
  MoreHorizontal,
  RotateCcw,
  Trash2,
  X,
  XCircle,
} from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";

import { EntryIcon } from "@/components/file-manager/icons";
import { EmptyState } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Backup, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { crumbs, formatSize, join } from "@/lib/files";
import { when } from "@/lib/format";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";
import { passkeyCancelled } from "@/lib/webauthn";

const kindNames: Record<string, string> = {
  manual: "Manual",
  scheduled: "Scheduled",
  safety: "Safety",
  final: "Final",
};

// What Wings' backup.activity returns (internal/wings/backup/activity.go).
type Activity = {
  job_id: string;
  kind: "backup" | "restore" | "extract";
  backup_id: string;
  backup_kind?: string;
  status: "queued" | "running" | "ok" | "failed";
  bytes: number;
  total: number;
  files?: number;
  error?: string;
  folder?: string;
  paths?: number;
  started_at: number;
  finished_at?: number;
};

// And backup.browse.
type BrowseEntry = {
  name: string;
  type: "file" | "dir" | "symlink";
  size: number;
  modified_at: number;
  denied?: boolean;
};

const busy = (a: Activity) => a.status === "queued" || a.status === "running";

function percent(a: Activity): number | undefined {
  if (a.status === "ok") return 100;
  if (!a.total) return undefined;
  return Math.min(99, Math.floor((a.bytes / a.total) * 100));
}

function backupDate(b: Backup | undefined): string {
  return b?.createdAt ? when(b.createdAt) : "a backup";
}

// ServerBackups lists a server's backups and takes, restores, locks, and
// deletes them, with what's running shown as it goes. A restore is the
// whole server (signed by the user's passkey: it replaces every file) or
// some files picked from the backup, put in .restore without touching
// anything else.
export function ServerBackups({
  orgId,
  nodeId,
  serverId,
  userId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
}) {
  const client = useQueryClient();
  const run = async <T,>(action: string, params: object): Promise<T> => {
    const res = await commandClient.execute({
      nodeId,
      action,
      serverId,
      paramsJson: JSON.stringify(params),
    });
    return (res.resultJson ? JSON.parse(res.resultJson) : {}) as T;
  };

  const activity = useQuery({
    queryKey: ["backup-activity", nodeId, serverId],
    queryFn: () => run<{ activity: Activity[] }>("backup.activity", {}),
    // Fast while something runs, slow otherwise.
    refetchInterval: (q) => (q.state.data?.activity.some(busy) ? 1_500 : 15_000),
  });
  const acts = activity.data?.activity ?? [];
  const anyBusy = acts.some(busy);
  const list = useConnectQuery(
    OrgService.method.listBackups,
    { orgId, nodeId, serverId },
    { refetchInterval: anyBusy ? 3_000 : 15_000 },
  );
  const backups = list.data?.backups ?? [];
  const byId = new Map(backups.map((b) => [b.id, b]));

  // When something finishes, the backups and files it changed are reloaded.
  const seen = useRef(new Set<string>());
  useEffect(() => {
    for (const a of acts) {
      if (busy(a) || seen.current.has(a.job_id)) continue;
      seen.current.add(a.job_id);
      void client.invalidateQueries();
    }
  }, [acts, client]);

  const [restoring, setRestoring] = useState<Backup>();
  const [pending, setPending] = useState("");
  const [error, setError] = useState("");

  async function act(key: string, fn: () => Promise<unknown>) {
    setPending(key);
    setError("");
    try {
      await fn();
      await Promise.all([activity.refetch(), client.invalidateQueries()]);
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setPending("");
    }
  }

  // Backups that were just asked for, before the node's report of them
  // arrives: shown as rows straight away.
  const newBackups = acts.filter((a) => a.kind === "backup" && busy(a) && !byId.has(a.backup_id));
  // Restores and pulls that finished in the last 10 minutes, as notes.
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const notes = acts.filter(
    (a) =>
      a.kind !== "backup" &&
      !busy(a) &&
      !dismissed.has(a.job_id) &&
      (a.finished_at ?? 0) > Date.now() - 10 * 60_000,
  );

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-4">
          <div className="space-y-1.5">
            <CardTitle>Backups</CardTitle>
            <CardDescription>
              Copies of the server's files. Restore all of it, or pull out just the files you need.
              Locked backups are kept until you unlock them.
            </CardDescription>
          </div>
          <Button
            size="sm"
            disabled={pending !== "" || acts.some((a) => a.kind === "backup" && busy(a))}
            onClick={() => act("create", () => run("backup.create", {}))}
          >
            {acts.some((a) => a.kind === "backup" && busy(a)) ? (
              <Loader2 className="animate-spin" />
            ) : (
              <Archive />
            )}
            Back up now
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {error && <p className="text-sm text-destructive">{error}</p>}
          {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
          {notes.map((a) => (
            <Note
              key={a.job_id}
              a={a}
              backup={byId.get(a.backup_id)}
              onDismiss={() => setDismissed(new Set(dismissed).add(a.job_id))}
              filesLink={(folder) => (
                <Link
                  to="/orgs/$orgId/servers/$nodeId/$serverId/files"
                  params={{ orgId, nodeId, serverId }}
                  search={{ path: folder }}
                  className="inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline"
                >
                  <FolderOpen className="size-3.5" /> Open folder
                </Link>
              )}
            />
          ))}
          {list.data && backups.length === 0 && newBackups.length === 0 ? (
            <EmptyState
              icon={Archive}
              title="No backups yet"
              description="Take one now, or add a schedule that does."
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Taken</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {newBackups.map((a) => (
                  <TableRow key={a.job_id}>
                    <TableCell>{new Date(a.started_at).toLocaleString()}</TableCell>
                    <TableCell>{kindNames[a.backup_kind ?? ""] ?? "—"}</TableCell>
                    <TableCell className="text-right text-muted-foreground">—</TableCell>
                    <TableCell>
                      <Working a={a} />
                    </TableCell>
                    <TableCell />
                  </TableRow>
                ))}
                {backups.map((b) => {
                  const running = acts.find((a) => a.backup_id === b.id && busy(a));
                  return (
                    <TableRow key={b.id}>
                      <TableCell>
                        <span className="flex items-center gap-1.5">
                          {b.locked && (
                            <Lock className="size-3.5 text-muted-foreground" aria-label="Locked" />
                          )}
                          {when(b.createdAt)}
                        </span>
                      </TableCell>
                      <TableCell>{kindNames[b.kind] ?? b.kind}</TableCell>
                      <TableCell className="text-right tabular-nums">
                        {b.status === "ok" ? formatSize(Number(b.size)) : "—"}
                      </TableCell>
                      <TableCell>
                        <BackupStatus backup={b} running={running} />
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger
                            render={
                              <Button
                                size="icon-sm"
                                variant="ghost"
                                aria-label={`Actions for the backup from ${when(b.createdAt)}`}
                                disabled={pending !== ""}
                              />
                            }
                          >
                            <MoreHorizontal />
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end" className="w-48">
                            <DropdownMenuItem
                              disabled={b.status !== "ok"}
                              onClick={() => setRestoring(b)}
                            >
                              <ArchiveRestore /> Restore…
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() =>
                                act(b.id, () =>
                                  b.locked
                                    ? sendSigned({
                                        userId,
                                        nodeId,
                                        serverId,
                                        action: "backup.lock",
                                        params: { backup_id: b.id, locked: false },
                                      })
                                    : run("backup.lock", { backup_id: b.id, locked: true }),
                                )
                              }
                            >
                              {b.locked ? <LockOpen /> : <Lock />}
                              {b.locked ? "Unlock" : "Lock"}
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              variant="destructive"
                              disabled={b.locked}
                              onClick={() => {
                                if (window.confirm("Delete this backup? It can't be undone."))
                                  void act(b.id, () =>
                                    sendSigned({
                                      userId,
                                      nodeId,
                                      serverId,
                                      action: "backup.delete",
                                      params: { backup_id: b.id },
                                    }),
                                  );
                              }}
                            >
                              <Trash2 /> {b.locked ? "Unlock it to delete" : "Delete"}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {restoring && (
        <RestoreDialog
          backup={restoring}
          run={run}
          onClose={() => setRestoring(undefined)}
          onWhole={() =>
            act(restoring.id, () =>
              sendSigned({
                userId,
                nodeId,
                serverId,
                action: "backup.restore",
                params: { backup_id: restoring.id },
              }),
            )
          }
          onPicked={async (paths) => {
            await run("backup.extract", { backup_id: restoring.id, paths });
            await activity.refetch();
          }}
        />
      )}
    </div>
  );
}

function BackupStatus({ backup, running }: { backup: Backup; running?: Activity }) {
  if (running) return <Working a={running} />;
  if (backup.status === "pending" || backup.status === "running") {
    return (
      <Working
        a={{
          job_id: "",
          kind: "backup",
          backup_id: backup.id,
          status: backup.status === "pending" ? "queued" : "running",
          bytes: 0,
          total: 0,
          started_at: 0,
        }}
      />
    );
  }
  if (backup.status === "failed") {
    return (
      <Badge variant="destructive" title={backup.error}>
        Failed
      </Badge>
    );
  }
  return backup.warning ? (
    <Badge variant="outline" title={backup.warning}>
      Done, with warnings
    </Badge>
  ) : (
    <Badge variant="secondary">Done</Badge>
  );
}

// Working is a row's status while something runs on it: what, how far,
// and a bar.
function Working({ a }: { a: Activity }) {
  const p = percent(a);
  const label =
    a.status === "queued"
      ? "Queued"
      : { backup: "Backing up", restore: "Restoring", extract: "Pulling files" }[a.kind];
  return (
    <span className="flex w-44 flex-col gap-1">
      <span className="flex items-center gap-2 text-sm">
        {a.status === "queued" ? (
          <Clock className="size-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <Loader2 className="size-3.5 shrink-0 animate-spin text-primary" />
        )}
        {label}
        {a.status === "running" && p !== undefined && (
          <span className="ml-auto text-xs text-muted-foreground tabular-nums">{p}%</span>
        )}
      </span>
      {a.status === "running" && (
        <span className="h-1 overflow-hidden rounded-full bg-muted">
          {p !== undefined ? (
            <span
              className="block h-full bg-primary transition-[width]"
              style={{ width: `${p}%` }}
            />
          ) : (
            <span className="block h-full w-1/3 animate-pulse rounded-full bg-primary/60" />
          )}
        </span>
      )}
    </span>
  );
}

// Note is a restore or a pull that just finished.
function Note({
  a,
  backup,
  onDismiss,
  filesLink,
}: {
  a: Activity;
  backup?: Backup;
  onDismiss: () => void;
  filesLink: (folder: string) => ReactNode;
}) {
  const items = `${a.paths ?? 0} ${a.paths === 1 ? "item" : "items"}`;
  const ok = a.status === "ok";
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-lg border px-3 py-2 text-sm",
        ok ? "border-emerald-500/30 bg-emerald-500/5" : "border-destructive/40 bg-destructive/5",
      )}
    >
      {ok ? (
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-500" />
      ) : (
        <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
      )}
      <div className="min-w-0 flex-1 space-y-0.5">
        <p>
          {a.kind === "restore"
            ? ok
              ? `Restored the backup from ${backupDate(backup)}.`
              : `Restoring the backup from ${backupDate(backup)} failed.`
            : ok
              ? `Pulled ${items} from the backup from ${backupDate(backup)}.`
              : `Pulling ${items} from the backup from ${backupDate(backup)} failed.`}
        </p>
        {ok && a.kind === "extract" && a.folder && (
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            They're in <code>{a.folder}</code> {filesLink(a.folder)}
          </p>
        )}
        {!ok && a.error && <p className="text-xs text-destructive">{a.error}</p>}
      </div>
      <Button size="icon-xs" variant="ghost" aria-label="Dismiss" onClick={onDismiss}>
        <X />
      </Button>
    </div>
  );
}

// RestoreDialog restores the whole server, or picks files from the backup.
function RestoreDialog({
  backup,
  run,
  onClose,
  onWhole,
  onPicked,
}: {
  backup: Backup;
  run: <T>(action: string, params: object) => Promise<T>;
  onClose: () => void;
  onWhole: () => Promise<void>;
  onPicked: (paths: string[]) => Promise<void>;
}) {
  const [mode, setMode] = useState<"whole" | "pick">();
  const [dir, setDir] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busyAct, setBusyAct] = useState(false);
  const [error, setError] = useState("");
  const listing = useQuery({
    queryKey: ["backup-browse", backup.id, dir],
    queryFn: () =>
      run<{ entries: BrowseEntry[] }>("backup.browse", { backup_id: backup.id, path: dir }),
    enabled: mode === "pick",
    staleTime: Number.POSITIVE_INFINITY,
  });
  const entries = [...(listing.data?.entries ?? [])].sort(
    (a, b) =>
      (a.type === "dir" ? 0 : 1) - (b.type === "dir" ? 0 : 1) ||
      a.name.localeCompare(b.name, undefined, { numeric: true }),
  );
  const inPicked = (p: string) => [...picked].some((x) => p === x || p.startsWith(`${x}/`));

  function toggle(p: string) {
    const next = new Set(picked);
    if (next.has(p)) next.delete(p);
    else {
      // Picking a folder covers what's in it.
      for (const x of next) if (x.startsWith(`${p}/`)) next.delete(x);
      next.add(p);
    }
    setPicked(next);
  }

  async function go(fn: () => Promise<void>) {
    setBusyAct(true);
    setError("");
    try {
      await fn();
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
      setBusyAct(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className={cn(mode === "pick" && "sm:max-w-2xl")}>
        <DialogHeader>
          <DialogTitle>Restore the backup from {when(backup.createdAt)}</DialogTitle>
          <DialogDescription>
            {formatSize(Number(backup.size))} · {Number(backup.files).toLocaleString()} files
          </DialogDescription>
        </DialogHeader>

        {!mode && (
          <div className="grid gap-3 sm:grid-cols-2">
            <ModeChoice
              icon={RotateCcw}
              title="The whole server"
              text="Replaces every file with the backup's. The server stops while it runs. A safety backup of the files now is taken first."
              onClick={() => setMode("whole")}
            />
            <ModeChoice
              icon={FileStack}
              title="Pick files"
              text="Choose files and folders. They're put in a .restore folder, and nothing on the server changes."
              onClick={() => setMode("pick")}
            />
          </div>
        )}

        {mode === "whole" && (
          <div className="space-y-2 text-sm">
            <p>
              Every file on the server is replaced with the backup's, and files made since are
              deleted. A safety backup of what's there now is taken first, so this can be undone.
            </p>
            <p className="text-muted-foreground">Your passkey signs it.</p>
          </div>
        )}

        {mode === "pick" && (
          <div className="flex min-h-0 flex-col gap-2">
            <nav
              aria-label="Folder in the backup"
              className="flex flex-wrap items-center gap-1 text-sm"
            >
              <button
                type="button"
                className="rounded px-1 font-medium hover:bg-muted"
                onClick={() => setDir("")}
              >
                Backup
              </button>
              {crumbs(dir).map((c) => (
                <span key={c.path} className="flex items-center gap-1">
                  <ChevronRight className="size-3.5 text-muted-foreground" />
                  <button
                    type="button"
                    className="rounded px-1 font-medium hover:bg-muted"
                    onClick={() => setDir(c.path)}
                  >
                    {c.name}
                  </button>
                </span>
              ))}
            </nav>
            <div className="max-h-80 overflow-auto rounded-lg border">
              {listing.isPending && (
                <p className="flex items-center gap-2 p-3 text-sm text-muted-foreground">
                  <Loader2 className="size-4 animate-spin" /> Opening the backup…
                </p>
              )}
              {listing.error && (
                <p className="p-3 text-sm text-destructive">{message(listing.error)}</p>
              )}
              {listing.data && entries.length === 0 && (
                <p className="p-3 text-sm text-muted-foreground">This folder is empty.</p>
              )}
              <ul className="divide-y text-sm">
                {entries.map((e) => {
                  const p = join(dir, e.name);
                  const covered = inPicked(p);
                  return (
                    <li
                      key={e.name}
                      className={cn("flex items-center gap-3 px-3 py-2", e.denied && "opacity-50")}
                    >
                      <Checkbox
                        aria-label={`Restore ${e.name}`}
                        disabled={e.denied || (covered && !picked.has(p))}
                        checked={covered}
                        onCheckedChange={() => toggle(p)}
                      />
                      {e.type === "dir" && !e.denied ? (
                        <button
                          type="button"
                          className="flex min-w-0 flex-1 items-center gap-2 text-left hover:underline"
                          onClick={() => setDir(p)}
                        >
                          <Folder className="size-4 shrink-0 text-sky-500" />
                          <span className="truncate">{e.name}</span>
                        </button>
                      ) : (
                        <span
                          className="flex min-w-0 flex-1 items-center gap-2"
                          title={
                            e.denied ? "The game's egg doesn't allow restoring this" : undefined
                          }
                        >
                          <EntryIcon e={{ ...e, mode: 0, type: e.type, denied: e.denied }} />
                          <span className="truncate">{e.name}</span>
                        </span>
                      )}
                      <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                        {formatSize(e.size)}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </div>
            <p className="text-xs text-muted-foreground">
              {picked.size === 0
                ? "Tick files and folders to restore; open a folder to pick inside it."
                : `${picked.size} picked: ${[...picked].slice(0, 3).join(", ")}${picked.size > 3 ? ", …" : ""}`}
            </p>
          </div>
        )}

        {error && <p className="text-sm text-destructive">{error}</p>}
        <DialogFooter>
          {mode ? (
            <Button variant="outline" onClick={() => setMode(undefined)}>
              Back
            </Button>
          ) : (
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
          )}
          {mode === "whole" && (
            <Button variant="destructive" disabled={busyAct} onClick={() => go(onWhole)}>
              Restore the whole server
            </Button>
          )}
          {mode === "pick" && (
            <Button
              disabled={busyAct || picked.size === 0}
              onClick={() => go(() => onPicked([...picked]))}
            >
              Restore {picked.size || ""} {picked.size === 1 ? "item" : "items"} to .restore
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ModeChoice({
  icon: Icon,
  title,
  text,
  onClick,
}: {
  icon: typeof RotateCcw;
  title: string;
  text: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-col items-start gap-2 rounded-lg border p-4 text-left transition-colors hover:border-primary hover:bg-muted/50"
    >
      <Icon className="size-5 text-muted-foreground" />
      <span className="text-sm font-medium">{title}</span>
      <span className="text-xs text-muted-foreground">{text}</span>
    </button>
  );
}
