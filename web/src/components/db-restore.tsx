import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import { FolderOpen, History, Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { UsageBar } from "@/components/stats";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import type { Database } from "@/lib/databases";
import { dbCommand, exportDir, formatBytes, type Transfer } from "@/lib/db-browser";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

// RestoreDatabaseDialog restores one of a server's databases from one of
// its backups that holds it (docs/DATABASES.md#backups), leaving the
// server's files alone. What the database has now is exported first.
export function RestoreDatabaseDialog({
  orgId,
  nodeId,
  serverId,
  userId,
  db,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  db: Database;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const list = useConnectQuery(OrgService.method.listBackups, { orgId, nodeId, serverId });
  const backups = (list.data?.backups ?? []).filter(
    (b) => b.status === "ok" && b.databases.includes(db.label),
  );
  const [picked, setPicked] = useState("");
  const [job, setJob] = useState<{ id: string; before: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const chosen = picked || backups[0]?.id || "";

  const status = useQuery({
    queryKey: ["db-transfer", nodeId, serverId, job?.id],
    queryFn: () => dbCommand<Transfer>(nodeId, serverId, "db.transfer.status", { job_id: job?.id }),
    enabled: !!job,
    refetchInterval: (q) =>
      q.state.data && ["succeeded", "failed", "cancelled"].includes(q.state.data.status)
        ? false
        : 1000,
  });
  const st = status.data;
  const over = st && ["succeeded", "failed", "cancelled"].includes(st.status);

  async function start(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await sendSigned({
        userId,
        nodeId,
        serverId,
        action: "db.restore",
        params: { id: db.id, backup_id: chosen },
      });
      const r = JSON.parse(res.resultJson) as { job_id: string; before: string };
      setJob({ id: r.job_id, before: r.before });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  function close() {
    if (st?.status === "succeeded") {
      void client.invalidateQueries({ queryKey: ["db-schema", nodeId, serverId, db.id] });
      void client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, db.id] });
    }
    onClose();
  }

  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent>
        <form onSubmit={start} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Restore {db.label} from a backup</DialogTitle>
            <DialogDescription>
              The database goes back to how it was in the backup; the server's files stay as they
              are. What it has now is exported to {exportDir} first, so you can import it again.
            </DialogDescription>
          </DialogHeader>

          {!job &&
            (list.error ? (
              <p className="text-sm text-destructive">{message(list.error)}</p>
            ) : !list.data ? (
              <p className="text-sm text-muted-foreground">Reading the backups…</p>
            ) : backups.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No backup holds {db.label} yet. Backups taken from now on include the server's
                databases.
              </p>
            ) : (
              <fieldset className="flex max-h-72 flex-col gap-1.5 overflow-y-auto">
                <legend className="sr-only">Backup</legend>
                {backups.map((b) => (
                  <label
                    key={b.id}
                    className={cn(
                      "flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2 text-sm transition-colors hover:bg-muted/50 has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                      chosen === b.id && "border-primary bg-brand-wash hover:bg-brand-wash",
                    )}
                  >
                    <input
                      type="radio"
                      name="backup"
                      className="sr-only"
                      checked={chosen === b.id}
                      onChange={() => setPicked(b.id)}
                    />
                    <span>{when(b.createdAt)}</span>
                    <span className="text-xs text-muted-foreground">
                      {b.kind === "scheduled"
                        ? "Scheduled"
                        : b.kind === "manual"
                          ? "Manual"
                          : b.kind}
                    </span>
                  </label>
                ))}
              </fieldset>
            ))}

          {job && (
            <div className="flex flex-col gap-2" role="status">
              {!over && (
                <>
                  <p className="flex items-center gap-2 text-sm">
                    <Loader2 className="size-3.5 animate-spin" /> Restoring…
                  </p>
                  {st?.total ? (
                    <UsageBar fraction={Math.min(1, st.bytes / st.total)} />
                  ) : st?.bytes ? (
                    <p className="text-xs text-muted-foreground">{formatBytes(st.bytes)}</p>
                  ) : null}
                </>
              )}
              {st?.status === "succeeded" && (
                <p className="text-sm">
                  Restored. What it had before is in{" "}
                  <code className="font-mono text-xs">/{job.before}</code>.
                </p>
              )}
              {st && (st.status === "failed" || st.status === "cancelled") && (
                <p className="text-sm text-destructive">
                  It didn't finish: {st.error || st.status}
                </p>
              )}
            </div>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            {job && over ? (
              <>
                <Link
                  to="/orgs/$orgId/servers/$serverId/files"
                  params={{ orgId, serverId }}
                  search={{ path: exportDir }}
                  className="inline-flex items-center gap-1.5 text-sm font-medium underline-offset-4 hover:underline"
                >
                  <FolderOpen className="size-4" /> Open the folder
                </Link>
                <Button type="button" onClick={close}>
                  Done
                </Button>
              </>
            ) : (
              <>
                <Button type="button" variant="outline" onClick={close}>
                  {job ? "Close" : "Cancel"}
                </Button>
                {!job && (
                  <Button type="submit" variant="destructive" disabled={busy || !chosen}>
                    {busy ? <Loader2 className="animate-spin" /> : <History />} Restore with passkey
                  </Button>
                )}
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
