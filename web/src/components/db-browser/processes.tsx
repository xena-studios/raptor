import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, RefreshCw, Square } from "lucide-react";
import { useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { dbCommand, type Process } from "@/lib/db-browser";
import { message } from "@/lib/errors";

// ProcessesTab lists the connections to the database (the ones its user
// can see: its own), and stops a running query. With instance, it's every
// connection to the instance, seen and stopped as its administrator
// (admins only).
export function ProcessesTab({
  nodeId,
  serverId,
  id,
  canWrite,
  instance,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  canWrite: boolean;
  // An instance's ID: every connection to it.
  instance?: string;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const key = instance
    ? ["db-instance-processes", nodeId, instance]
    : ["db-processes", nodeId, serverId, id];
  const list = useQuery({
    queryKey: key,
    queryFn: async () =>
      (
        await dbCommand<{ processes: Process[] }>(
          nodeId,
          instance ? "" : serverId,
          instance ? "node.db.processes" : "db.processes",
          { id: instance ?? id },
        )
      ).processes,
    refetchInterval: 5_000,
    retry: false,
  });
  const [busy, setBusy] = useState(0);
  const [error, setError] = useState("");

  async function stop(p: Process) {
    const ok = await ask.confirm({
      title: "Stop this query?",
      description: "Its connection stays; whatever sent it gets an error.",
      confirm: "Stop query",
      destructive: true,
    });
    if (!ok) return;
    setBusy(p.id);
    setError("");
    try {
      if (instance) {
        await dbCommand(nodeId, "", "node.db.process.kill", { id: instance, process: p.id });
      } else {
        await dbCommand(nodeId, serverId, "db.process.kill", { id, process: p.id });
      }
      await client.invalidateQueries({ queryKey: key });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(0);
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {instance
            ? "Every connection to this instance, from every server. Refreshed every few seconds."
            : "Connections to this database, like the server's own. Refreshed every few seconds."}
        </p>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Refresh"
          onClick={() => client.invalidateQueries({ queryKey: key })}
        >
          <RefreshCw className={list.isFetching ? "animate-spin" : undefined} />
        </Button>
      </div>
      {(list.error || error) && (
        <p className="text-sm text-destructive">{error || message(list.error)}</p>
      )}
      {list.data &&
        (list.data.length === 0 ? (
          <p className="rounded-lg border p-6 text-center text-sm text-muted-foreground">
            Nothing is connected right now.
          </p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  {instance && <TableHead>User</TableHead>}
                  {instance && <TableHead>Database</TableHead>}
                  <TableHead>State</TableHead>
                  <TableHead className="text-right">For</TableHead>
                  <TableHead>Query</TableHead>
                  {canWrite && <TableHead className="w-10" />}
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.data.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="font-mono text-xs">{p.id}</TableCell>
                    {instance && <TableCell className="font-mono text-xs">{p.user}</TableCell>}
                    {instance && (
                      <TableCell className="font-mono text-xs">
                        {p.database || <span className="text-muted-foreground">—</span>}
                      </TableCell>
                    )}
                    <TableCell className="text-xs">{p.state || "idle"}</TableCell>
                    <TableCell className="text-right text-xs tabular-nums">{p.seconds} s</TableCell>
                    <TableCell className="max-w-[28rem] truncate font-mono text-xs" title={p.query}>
                      {p.query || <span className="text-muted-foreground">—</span>}
                    </TableCell>
                    {canWrite && (
                      <TableCell>
                        {p.query && (
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Stop query ${p.id}`}
                            disabled={busy !== 0}
                            onClick={() => stop(p)}
                          >
                            {busy === p.id ? <Loader2 className="animate-spin" /> : <Square />}
                          </Button>
                        )}
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ))}
    </div>
  );
}
