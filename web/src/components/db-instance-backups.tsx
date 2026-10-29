import { useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import { Archive, History, Loader2, Settings2, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Badge } from "@/components/ui/badge";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Database, DBInstance } from "@/lib/databases";
import { dbCommand, formatBytes } from "@/lib/db-browser";
import {
  type InstanceBackup,
  type InstanceBackupSettings,
  inProgress,
  instanceBackupsKey,
  kindNames,
  type InstanceBackups as Result,
  settingsNeedPasskey,
} from "@/lib/db-instance-backups";
import { message } from "@/lib/errors";
import { dateTime } from "@/lib/format";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// InstanceBackups is a database instance's own backups (docs/DATABASES.md#backups):
// every database on it, daily and by hand, to one destination; one
// database restored from any of them; and the schedule, how many to keep,
// and where. Admins only, like the rest of an instance.
export function InstanceBackups({
  nodeId,
  userId,
  instance,
}: {
  nodeId: string;
  userId: string;
  instance: DBInstance;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const key = instanceBackupsKey(nodeId, instance.id);
  const res = useQuery({
    queryKey: key,
    queryFn: () => dbCommand<Result>(nodeId, "", "node.db.backups", { instance_id: instance.id }),
    refetchInterval: (q) => (q.state.data && inProgress(q.state.data.backups) ? 3000 : 30_000),
  });
  const [editing, setEditing] = useState(false);
  const [restoring, setRestoring] = useState<InstanceBackup>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const data = res.data;
  const destName = (id: string) =>
    data?.destinations.find((d) => d.id === id)?.name ?? (id === "local" ? "This node" : id);

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: key });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const backUp = () =>
    act(() =>
      commandClient.execute({
        nodeId,
        action: "node.db.backup.create",
        paramsJson: JSON.stringify({ instance_id: instance.id }),
      }),
    );

  async function remove(b: InstanceBackup) {
    const ok = await ask.confirm({
      title: "Delete this backup?",
      description: `The backup from ${dateTime(new Date(b.created_at))} is deleted from ${destName(b.destination_id)}. It can't be restored from after.`,
      confirm: "Delete with passkey",
      destructive: true,
    });
    if (!ok) return;
    await act(() =>
      sendSigned({
        userId,
        nodeId,
        action: "node.db.backup.delete",
        params: { instance_id: instance.id, id: b.id },
      }),
    );
  }

  const s = data?.settings;
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <CardTitle>Backups</CardTitle>
          <CardDescription>
            {!s
              ? "Every database on it, taken as each database's own user."
              : s.enabled
                ? `Every database on it, daily at ${s.at} (the node's time), keeping the last ${s.keep_last} on ${destName(s.destination_id)}. Servers' backups still hold their own databases too.`
                : `Daily backups are off. Backups taken by hand go to ${destName(s.destination_id)}, keeping the last ${s.keep_last}.`}
          </CardDescription>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={!data} onClick={() => setEditing(true)}>
            <Settings2 /> Settings
          </Button>
          <Button size="sm" disabled={busy || !data} onClick={backUp}>
            {busy ? <Loader2 className="animate-spin" /> : <Archive />} Back up now
          </Button>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {res.error && <p className="text-sm text-destructive">{message(res.error)}</p>}
        {error && <p className="text-sm text-destructive">{error}</p>}
        {res.isPending && <p className="text-sm text-muted-foreground">Asking the node…</p>}
        {data && data.backups.length === 0 && (
          <p className="text-sm text-muted-foreground">
            No backups yet.{" "}
            {data.settings.enabled ? `The first is at ${data.settings.at}.` : "Back it up now."}
          </p>
        )}
        {data && data.backups.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Taken</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead>Databases</TableHead>
                <TableHead>Where</TableHead>
                <TableHead className="text-right">Size</TableHead>
                <TableHead className="w-0" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.backups.map((b) => (
                <TableRow key={b.id}>
                  <TableCell className="whitespace-nowrap">
                    {dateTime(new Date(b.created_at))}
                    {b.status === "failed" && (
                      <span className="block max-w-80 text-xs text-destructive">
                        Failed: {b.error}
                      </span>
                    )}
                    {b.status === "ok" && b.warning && (
                      <span className="block max-w-80 text-xs text-warn">{b.warning}</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-1.5">
                      {kindNames[b.kind] ?? b.kind}
                      {(b.status === "pending" || b.status === "running") && (
                        <Badge variant="outline">
                          <Loader2 className="size-3 animate-spin" />
                          {b.status === "pending" ? "Queued" : "Running"}
                        </Badge>
                      )}
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {b.status === "ok" ? (b.databases ?? []).join(", ") || "None" : "—"}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {destName(b.destination_id)}
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                    {b.status === "ok" ? formatBytes(b.size) : "—"}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={b.status !== "ok" || !(b.databases ?? []).length}
                        onClick={() => setRestoring(b)}
                      >
                        <History /> Restore
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Delete backup"
                        disabled={
                          busy ||
                          b.status === "pending" ||
                          b.status === "running" ||
                          b.kind === "recovered"
                        }
                        onClick={() => remove(b)}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
      {editing && data && (
        <SettingsDialog
          nodeId={nodeId}
          userId={userId}
          instanceId={instance.id}
          data={data}
          onClose={() => setEditing(false)}
        />
      )}
      {restoring && (
        <RestoreDialog
          nodeId={nodeId}
          userId={userId}
          instance={instance}
          backup={restoring}
          onClose={() => setRestoring(undefined)}
        />
      )}
    </Card>
  );
}

function SettingsDialog({
  nodeId,
  userId,
  instanceId,
  data,
  onClose,
}: {
  nodeId: string;
  userId: string;
  instanceId: string;
  data: Result;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [next, setNext] = useState<InstanceBackupSettings>(data.settings);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const signed = settingsNeedPasskey(next, data.settings);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const params = { instance_id: instanceId, ...next };
    try {
      if (signed) {
        await sendSigned({ userId, nodeId, action: "node.db.backup.settings", params });
      } else {
        await commandClient.execute({
          nodeId,
          action: "node.db.backup.settings",
          paramsJson: JSON.stringify(params),
        });
      }
      await client.invalidateQueries({ queryKey: instanceBackupsKey(nodeId, instanceId) });
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={save} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Backup settings</DialogTitle>
            <DialogDescription>
              Backups already taken stay where they are; how many to keep applies from the next one.
            </DialogDescription>
          </DialogHeader>
          <Label className="flex items-center justify-between gap-3 font-normal">
            Back up daily
            <Switch
              checked={next.enabled}
              onCheckedChange={(on) => setNext({ ...next, enabled: on })}
            />
          </Label>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5">
              <Label htmlFor="ib-at">Time (the node's)</Label>
              <Input
                id="ib-at"
                type="time"
                required
                value={next.at}
                disabled={!next.enabled}
                onChange={(e) => setNext({ ...next, at: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="ib-keep">Keep the last</Label>
              <Input
                id="ib-keep"
                type="number"
                min={1}
                max={100}
                required
                value={next.keep_last}
                onChange={(e) => setNext({ ...next, keep_last: Number(e.target.value) })}
              />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="ib-dest">Where</Label>
            <select
              id="ib-dest"
              className={select}
              value={next.destination_id}
              onChange={(e) => setNext({ ...next, destination_id: e.target.value })}
            >
              {data.destinations.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.id === "local" ? "This node's disk" : d.name}
                </option>
              ))}
            </select>
            <p className="text-xs text-muted-foreground">
              Somewhere off the node outlives it. Destinations are added in the node's backup
              settings.
            </p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              {signed ? "Save with passkey" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RestoreDialog({
  nodeId,
  userId,
  instance,
  backup,
  onClose,
}: {
  nodeId: string;
  userId: string;
  instance: DBInstance;
  backup: InstanceBackup;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const dbs = useQuery({
    queryKey: ["db-instance-backup-databases", nodeId, instance.id],
    queryFn: async () =>
      (
        await dbCommand<{ databases: Database[] }>(nodeId, "", "node.db.databases", {
          instance_id: instance.id,
          id: instance.id,
        })
      ).databases,
  });
  // The backup names its databases by their names on the instance.
  const choices = (dbs.data ?? []).filter((d) => (backup.databases ?? []).includes(d.name));
  const [picked, setPicked] = useState("");
  const chosen = picked || choices[0]?.id || "";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [started, setStarted] = useState(false);

  async function start(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "node.db.backup.restore",
        params: { instance_id: instance.id, id: backup.id, database_id: chosen },
      });
      setStarted(true);
      await client.invalidateQueries({ queryKey: instanceBackupsKey(nodeId, instance.id) });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={start} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Restore from {dateTime(new Date(backup.created_at))}</DialogTitle>
            <DialogDescription>
              The database goes back to how it was in this backup, and anything written to it since
              is lost. A safety backup of {instance.name} is taken first, so you can restore from it
              to undo this.
            </DialogDescription>
          </DialogHeader>
          {started ? (
            <p className="text-sm" role="status">
              The restore started: the safety backup first, then the database. It shows in the
              backups list.
            </p>
          ) : dbs.error ? (
            <p className="text-sm text-destructive">{message(dbs.error)}</p>
          ) : !dbs.data ? (
            <p className="text-sm text-muted-foreground">Asking the node…</p>
          ) : choices.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              None of the databases in this backup are on {instance.name} any more.
            </p>
          ) : (
            <fieldset className="flex max-h-72 flex-col gap-1.5 overflow-y-auto">
              <legend className="mb-1.5 text-sm font-medium">Database</legend>
              {choices.map((d) => (
                <label
                  key={d.id}
                  className={cn(
                    "flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2 text-sm transition-colors hover:bg-muted/50 has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                    chosen === d.id && "border-primary bg-brand-wash hover:bg-brand-wash",
                  )}
                >
                  <input
                    type="radio"
                    name="database"
                    className="sr-only"
                    checked={chosen === d.id}
                    onChange={() => setPicked(d.id)}
                  />
                  <span className="font-mono text-xs">{d.name}</span>
                </label>
              ))}
            </fieldset>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {started ? "Done" : "Cancel"}
            </Button>
            {!started && (
              <Button type="submit" variant="destructive" disabled={busy || !chosen}>
                {busy ? <Loader2 className="animate-spin" /> : <History />} Restore with passkey
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
