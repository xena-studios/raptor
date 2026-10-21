import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import {
  Archive,
  ArchiveRestore,
  CheckCircle2,
  ChevronDown,
  Clock,
  FolderOpen,
  Loader2,
  Lock,
  LockOpen,
  MoreHorizontal,
  Trash2,
  X,
  XCircle,
} from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";

import { BackupFiles } from "@/components/backup-files";
import { EmptyState } from "@/components/page";
import { BackupPlan, useBackupPlan } from "@/components/server-backup-plan";
import { StatusBadge } from "@/components/server-status";
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
import { formatSize } from "@/lib/files";
import { dateTime, isAdmin, when } from "@/lib/format";
import { shortId } from "@/lib/short-id";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const kindNames: Record<string, string> = {
  manual: "Manual",
  scheduled: "Scheduled",
  safety: "Safety",
  final: "Final",
  recovered: "Recovered",
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

const busy = (a: Activity) => a.status === "queued" || a.status === "running";

function percent(a: Activity): number | undefined {
  if (a.status === "ok") return 100;
  if (!a.total) return undefined;
  return Math.min(99, Math.floor((a.bytes / a.total) * 100));
}

function backupDate(b: Backup | undefined): string {
  return b?.createdAt ? when(b.createdAt) : "a backup";
}

// Which backup is open, and where in it: in the page's URL.
export type BrowseAt = { backup: string; path?: string; file?: string };

// ServerBackups lists a server's backups and takes, opens, locks, and
// deletes them, with what's running shown as it goes. Opening one shows
// its files like the file manager (BackupFiles), to restore some of them
// into .restore or the whole server.
export function ServerBackups({
  orgId,
  nodeId,
  serverId,
  userId,
  browse,
  onBrowse,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  browse?: BrowseAt;
  onBrowse: (to: BrowseAt | undefined) => void;
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
  const orgs = useConnectQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const plan = useBackupPlan(nodeId, serverId);
  const destName = (id: string) =>
    plan.data?.destinations.find((d) => d.id === id)?.name ??
    (id === "local" ? "Local" : "A removed destination");
  const targets = plan.data?.policy.targets ?? [];
  const backingUp = acts.some((a) => a.kind === "backup" && busy(a));

  // When something finishes, the backups and files it changed are reloaded.
  const seen = useRef(new Set<string>());
  useEffect(() => {
    for (const a of acts) {
      if (busy(a) || seen.current.has(a.job_id)) continue;
      seen.current.add(a.job_id);
      void client.invalidateQueries();
    }
  }, [acts, client]);

  const [deleting, setDeleting] = useState<Backup>();
  // Restores to .restore started from an open backup, by job.
  const [pulled, setPulled] = useState<Set<string>>(new Set());
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

  if (browse) {
    const open = backups.find((b) => b.id === browse.backup || shortId(b.id) === browse.backup);
    if (!open) {
      return list.isPending ? null : (
        <EmptyState
          icon={Archive}
          title="That backup isn't here"
          description="It may have been deleted, or it's still being taken."
          action={<Button onClick={() => onBrowse(undefined)}>Back to the backups</Button>}
        />
      );
    }
    return (
      <BackupFiles
        orgId={orgId}
        nodeId={nodeId}
        serverId={serverId}
        backup={open}
        path={browse.path ?? ""}
        file={browse.file}
        pulls={acts.filter((a) => pulled.has(a.job_id))}
        onNavigate={(to) => onBrowse({ backup: browse.backup, ...to })}
        onClose={() => onBrowse(undefined)}
        onPull={async (paths) => {
          const r = await run<{ job_id: string }>("backup.extract", {
            backup_id: open.id,
            paths,
          });
          setPulled(new Set(pulled).add(r.job_id));
          await activity.refetch();
        }}
        onRestoreAll={async () => {
          await sendSigned({
            userId,
            nodeId,
            serverId,
            action: "backup.restore",
            params: { backup_id: open.id },
          });
          await activity.refetch();
          onBrowse(undefined);
        }}
      />
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 basis-xs space-y-1.5">
            <CardTitle>Backups</CardTitle>
            <CardDescription>
              Copies of the server's files. Open one to look through it, restore the files you need,
              or restore all of it. Locked backups are kept until you unlock them.
            </CardDescription>
          </div>
          <div className="flex">
            <Button
              size="sm"
              className={targets.length > 1 ? "rounded-r-none" : undefined}
              disabled={pending !== "" || backingUp}
              onClick={() => act("create", () => run("backup.create", {}))}
            >
              {backingUp ? <Loader2 className="animate-spin" /> : <Archive />}
              Back up now
            </Button>
            {targets.length > 1 && (
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      size="icon-sm"
                      className="rounded-l-none border-l border-primary-foreground/20"
                      aria-label="Back up to one place"
                      disabled={pending !== "" || backingUp}
                    />
                  }
                >
                  <ChevronDown />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-56">
                  {targets.map((t) => (
                    <DropdownMenuItem
                      key={t.destination_id}
                      onClick={() =>
                        act("create", () =>
                          run("backup.create", { destination_id: t.destination_id }),
                        )
                      }
                    >
                      Back up to {destName(t.destination_id)} only
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
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
                  to="/orgs/$orgId/servers/$serverId/files"
                  params={{ orgId, serverId }}
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
                  <TableHead>Where</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {newBackups.map((a) => (
                  <TableRow key={a.job_id}>
                    <TableCell>{dateTime(new Date(a.started_at))}</TableCell>
                    <TableCell>{kindNames[a.backup_kind ?? ""] ?? "—"}</TableCell>
                    <TableCell className="text-muted-foreground">—</TableCell>
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
                      <TableCell>
                        {kindNames[b.kind] ?? b.kind}
                        {b.databases.length > 0 && (
                          <span
                            className="block max-w-40 truncate text-xs text-muted-foreground"
                            title={`With the databases ${b.databases.join(", ")}`}
                          >
                            With {b.databases.join(", ")}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="max-w-40 truncate">
                        {destName(b.destinationId || "local")}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs tabular-nums">
                        {b.status === "ok" ? formatSize(Number(b.size)) : "—"}
                      </TableCell>
                      <TableCell>
                        <BackupStatus backup={b} running={running} />
                      </TableCell>
                      <TableCell>
                        <span className="flex items-center justify-end gap-1">
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={b.status !== "ok" || pending !== ""}
                            onClick={() => onBrowse({ backup: shortId(b.id) })}
                          >
                            <ArchiveRestore /> Restore
                          </Button>
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
                                disabled={b.locked || b.kind === "recovered"}
                                onClick={() => setDeleting(b)}
                              >
                                <Trash2 /> {b.locked ? "Unlock it to delete" : "Delete"}
                              </DropdownMenuItem>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </span>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <BackupPlan orgId={orgId} nodeId={nodeId} serverId={serverId} userId={userId} admin={admin} />

      <Dialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete the backup from {backupDate(deleting)}?</DialogTitle>
            <DialogDescription>
              It's deleted for good, wherever it's stored; backups don't go to a trash. Your passkey
              signs it.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(undefined)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const b = deleting;
                setDeleting(undefined);
                if (b)
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
              <Trash2 /> Delete backup
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
      <span title={backup.error}>
        <StatusBadge status={{ label: "Failed", tone: "failed" }} />
      </span>
    );
  }
  return backup.warning ? (
    <span title={backup.warning}>
      <StatusBadge status={{ label: "Done, with warnings", tone: "pending" }} />
    </span>
  ) : (
    <StatusBadge status={{ label: "Done", tone: "live" }} />
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
        ok ? "border-ok/30 bg-ok-wash" : "border-destructive/40 bg-danger-wash",
      )}
    >
      {ok ? (
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" />
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
              ? `Restored ${items} from the backup from ${backupDate(backup)}.`
              : `Restoring ${items} from the backup from ${backupDate(backup)} failed.`}
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
