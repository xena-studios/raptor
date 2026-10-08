import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Archive, Lock, LockOpen } from "lucide-react";
import { useState } from "react";

import { EmptyState } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { when } from "@/lib/format";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const kindNames: Record<string, string> = {
  manual: "Manual",
  scheduled: "Scheduled",
  safety: "Safety",
  final: "Final",
};

function BackupStatus({ backup }: { backup: Backup }) {
  switch (backup.status) {
    case "ok":
      return backup.warning ? (
        <Badge variant="outline" title={backup.warning}>
          Done, with warnings
        </Badge>
      ) : (
        <Badge variant="secondary">Done</Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" title={backup.error}>
          Failed
        </Badge>
      );
    default:
      return <Badge variant="outline">Running…</Badge>;
  }
}

// ServerBackups lists a server's backups and takes, restores, locks, and
// deletes them. Restoring, deleting, and unlocking can lose data, so the
// user's passkey signs them.
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
  const list = useQuery(
    OrgService.method.listBackups,
    { orgId, nodeId, serverId },
    { refetchInterval: 5_000 },
  );
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const backups = list.data?.backups ?? [];

  async function run(
    key: string,
    action: string,
    params: Record<string, unknown>,
    signed: boolean,
    done?: string,
  ) {
    setBusy(key);
    setError("");
    setNotice("");
    try {
      if (signed) {
        await sendSigned({ userId, nodeId, action, serverId, params });
      } else {
        await commandClient.execute({
          nodeId,
          action,
          serverId,
          paramsJson: JSON.stringify(params),
        });
      }
      if (done) setNotice(done);
      await client.invalidateQueries();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
          <CardTitle>Backups</CardTitle>
          <CardDescription>
            Copies of the server's files. Locked backups are kept until you unlock them.
          </CardDescription>
        </div>
        <Button
          size="sm"
          disabled={busy !== ""}
          onClick={() => run("create", "backup.create", {}, false, "The backup started.")}
        >
          <Archive /> Back up now
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {error && <p className="text-sm text-destructive">{error}</p>}
        {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
        {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
        {list.data && backups.length === 0 ? (
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
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {backups.map((b) => (
                <TableRow key={b.id}>
                  <TableCell>
                    <span className="flex items-center gap-1.5">
                      {b.locked && <Lock className="size-3.5 text-muted-foreground" />}
                      {when(b.createdAt)}
                    </span>
                  </TableCell>
                  <TableCell>{kindNames[b.kind] ?? b.kind}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {b.status === "ok" ? formatSize(Number(b.size)) : "—"}
                  </TableCell>
                  <TableCell>
                    <BackupStatus backup={b} />
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busy !== "" || b.status !== "ok"}
                        onClick={() => {
                          if (
                            window.confirm(
                              "Restore this backup? The server's files are replaced with it (a safety backup is taken first).",
                            )
                          )
                            run(
                              b.id,
                              "backup.restore",
                              { backup_id: b.id },
                              true,
                              "The restore started.",
                            );
                        }}
                      >
                        Restore
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={b.locked ? "Unlock" : "Lock"}
                        title={b.locked ? "Unlock" : "Lock"}
                        disabled={busy !== ""}
                        onClick={() =>
                          run(b.id, "backup.lock", { backup_id: b.id, locked: !b.locked }, b.locked)
                        }
                      >
                        {b.locked ? <LockOpen /> : <Lock />}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        disabled={busy !== "" || b.locked}
                        title={b.locked ? "Unlock it first" : undefined}
                        onClick={() => {
                          if (window.confirm("Delete this backup? It can't be undone."))
                            run(b.id, "backup.delete", { backup_id: b.id }, true);
                        }}
                      >
                        Delete
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
