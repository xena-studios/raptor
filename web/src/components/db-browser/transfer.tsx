import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Download, FolderOpen, Loader2, Upload } from "lucide-react";
import { type FormEvent, useState } from "react";

import { UsageBar } from "@/components/stats";
import { Button, buttonVariants } from "@/components/ui/button";
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
import {
  dbCommand,
  exportDir,
  formatBytes,
  type Table,
  type Transfer,
  tableKey,
} from "@/lib/db-browser";
import { message } from "@/lib/errors";

// TransferDialog imports a .sql file the server has into the database, or
// exports the database or a table into the server's database-exports
// folder, then follows the job until it's done.
export function TransferDialog({
  orgId,
  nodeId,
  serverId,
  id,
  kind,
  table,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  id: string;
  kind: "import" | "export";
  table?: Table;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [path, setPath] = useState("");
  const [what, setWhat] = useState<"database" | "table-sql" | "table-csv">("database");
  const [jobID, setJobID] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const status = useQuery({
    queryKey: ["db-transfer", nodeId, serverId, jobID],
    queryFn: () => dbCommand<Transfer>(nodeId, serverId, "db.transfer.status", { job_id: jobID }),
    enabled: !!jobID,
    refetchInterval: (q) =>
      q.state.data && ["succeeded", "failed", "cancelled"].includes(q.state.data.status)
        ? false
        : 1000,
  });
  const st = status.data;
  const done = st?.status === "succeeded";
  const over = st && ["succeeded", "failed", "cancelled"].includes(st.status);

  async function start(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res =
        kind === "import"
          ? await dbCommand<{ job_id: string }>(nodeId, serverId, "db.import", {
              id,
              path: path.trim().replace(/^\/+/, ""),
            })
          : await dbCommand<{ job_id: string }>(nodeId, serverId, "db.export", {
              id,
              schema: what === "database" ? undefined : table?.schema,
              table: what === "database" ? undefined : table?.name,
              format: what === "table-csv" ? "csv" : "sql",
            });
      setJobID(res.job_id);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    try {
      await dbCommand(nodeId, serverId, "db.transfer.cancel", { job_id: jobID });
    } catch (err) {
      setError(message(err));
    }
  }

  function close() {
    if (done && kind === "import") {
      void client.invalidateQueries({ queryKey: ["db-schema", nodeId, serverId, id] });
      void client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, id] });
    }
    onClose();
  }

  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent>
        <form onSubmit={start} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{kind === "import" ? "Import a .sql file" : "Export"}</DialogTitle>
            <DialogDescription>
              {kind === "import"
                ? "Runs a .sql or .sql.gz file from the server's files against this database, as its own user. Upload the file in the file manager first."
                : `Writes the dump to the server's ${exportDir} folder, to download from the file manager.`}
            </DialogDescription>
          </DialogHeader>

          {!jobID &&
            (kind === "import" ? (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="import-path">File</Label>
                <Input
                  id="import-path"
                  autoFocus
                  className="font-mono"
                  placeholder="backups/luckperms.sql.gz"
                  value={path}
                  onChange={(e) => setPath(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Its path in the server's files. It stops at the first error; what ran before it
                  stays.
                </p>
              </div>
            ) : (
              <fieldset className="flex flex-col gap-2">
                <legend className="sr-only">What to export</legend>
                {[
                  { v: "database", label: "The whole database, as .sql.gz", show: true },
                  {
                    v: "table-sql",
                    label: `Table ${table ? tableKey(table) : ""}, as .sql.gz`,
                    show: !!table,
                  },
                  {
                    v: "table-csv",
                    label: `Table ${table ? tableKey(table) : ""}, as .csv`,
                    show: !!table,
                  },
                ]
                  .filter((o) => o.show)
                  .map((o) => (
                    <label key={o.v} className="flex items-center gap-2 text-sm">
                      <input
                        type="radio"
                        name="export-what"
                        className="accent-primary"
                        checked={what === o.v}
                        onChange={() => setWhat(o.v as typeof what)}
                      />
                      {o.label}
                    </label>
                  ))}
              </fieldset>
            ))}

          {st && (
            <div className="flex flex-col gap-2">
              {st.total ? <UsageBar fraction={Math.min(1, st.bytes / st.total)} /> : null}
              <p className="text-sm">
                {
                  {
                    queued: "Waiting to start…",
                    running: kind === "import" ? "Importing…" : "Exporting…",
                    succeeded: kind === "import" ? "Imported." : "Exported.",
                    failed: "It failed.",
                    cancelled: "Stopped.",
                  }[st.status]
                }{" "}
                <span className="text-muted-foreground">
                  {formatBytes(st.bytes)}
                  {st.total ? ` of ${formatBytes(st.total)}` : ""}
                </span>
              </p>
              {st.error && (
                <p className="font-mono text-sm whitespace-pre-wrap text-destructive">{st.error}</p>
              )}
              {done && kind === "export" && (
                <p className="font-mono text-xs text-muted-foreground">/{st.path}</p>
              )}
            </div>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}

          <DialogFooter>
            {!jobID && (
              <>
                <Button type="button" variant="outline" onClick={close}>
                  Cancel
                </Button>
                <Button type="submit" disabled={busy || (kind === "import" && !path.trim())}>
                  {busy && <Loader2 className="animate-spin" />}
                  {kind === "import" ? (
                    <>
                      <Upload /> Import
                    </>
                  ) : (
                    <>
                      <Download /> Export
                    </>
                  )}
                </Button>
              </>
            )}
            {jobID && !over && (
              <Button type="button" variant="outline" onClick={cancel}>
                Stop
              </Button>
            )}
            {done && kind === "export" && (
              <Link
                to="/orgs/$orgId/servers/$serverId/files"
                params={{ orgId, serverId }}
                search={{ path: exportDir }}
                className={buttonVariants({ variant: "outline" })}
              >
                <FolderOpen /> Open the folder
              </Link>
            )}
            {over && (
              <Button type="button" onClick={close}>
                Done
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
