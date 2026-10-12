import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, HardDrive, Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { UsageBar } from "@/components/stats";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  type BlockDevice,
  type DataDisk,
  type NodeDisks as Disks,
  diskName,
  diskSpace,
  useLabels,
  useNodeDisks,
} from "@/lib/disks";
import { message } from "@/lib/errors";
import { formatBytes } from "@/lib/metrics";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";
import { passkeyCancelled } from "@/lib/webauthn";

// The node's Disks tab: where server data and backups go, which disk new
// servers use, and setting up empty disks for Raptor (formatting, so an
// owner's passkey signs it; Wings only formats empty disks).
export function NodeDisks({
  nodeId,
  userId,
  connected,
}: {
  nodeId: string;
  userId: string;
  connected: boolean;
}) {
  const disks = useNodeDisks(nodeId, connected);
  const [setup, setSetup] = useState<BlockDevice>();
  if (!connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          The node is offline, so its disks can't be read until it's back.
        </CardContent>
      </Card>
    );
  }
  if (disks.error) return <p className="text-sm text-destructive">{message(disks.error)}</p>;
  const d = disks.data;
  if (!d) return <p className="text-sm text-muted-foreground">Asking the node…</p>;
  const servers = d.data.filter((x) => x.role === "servers");
  const backups = d.data.filter((x) => x.role === "backups");
  return (
    <div className="flex flex-col gap-6">
      <ServerData nodeId={nodeId} d={d} disks={servers} />
      {backups.length > 0 && <BackupDisks disks={backups} />}
      <Devices devices={d.devices ?? []} onSetup={setSetup} />
      {setup && (
        <SetupDialog
          nodeId={nodeId}
          userId={userId}
          device={setup}
          onClose={() => setSetup(undefined)}
        />
      )}
    </div>
  );
}

function ServerData({ nodeId, d, disks }: { nodeId: string; d: Disks; disks: DataDisk[] }) {
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function makeDefault(id: string) {
    setBusy(id);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "node.disk.default",
        paramsJson: JSON.stringify({ disk: id }),
      });
      await client.invalidateQueries({ queryKey: ["node-disks", nodeId] });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Server data</CardTitle>
        <CardDescription>
          Where servers keep their files. New servers go on the default disk unless you pick another
          when creating them; a server stays on its disk for good.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {disks.map((x) => {
          const isDefault = d.default === x.id;
          return (
            <div key={x.id} className="flex flex-col gap-2 rounded-lg border p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                  <HardDrive className="size-4 shrink-0 text-muted-foreground" />
                  <span className="truncate font-medium">{diskName(x)}</span>
                  {isDefault && <Badge>Default</Badge>}
                </div>
                {!isDefault && x.mounted && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!!busy}
                    onClick={() => makeDefault(x.id)}
                  >
                    {busy === x.id && <Loader2 className="animate-spin" />} Make default
                  </Button>
                )}
              </div>
              {x.mounted ? (
                <UsageBar fraction={x.total ? (x.total - x.free) / x.total : 0} />
              ) : (
                <p className="flex items-center gap-1.5 text-sm text-destructive">
                  <AlertTriangle className="size-4" /> It isn't mounted, so its servers can't start.
                  Check it's still attached, then <code>sudo systemctl start</code> its mount, or
                  reboot.
                </p>
              )}
              <p className="text-xs text-muted-foreground">
                {diskSpace(x)} · {x.servers} {x.servers === 1 ? "server" : "servers"} ·{" "}
                <span className="font-mono">{x.mountpoint}</span>
              </p>
              {x.image && (
                <p className="text-xs text-muted-foreground">
                  An image file on the system disk: game saves write at about half the speed of a
                  disk of their own. Set up an empty disk below and make it the default to give new
                  servers full speed.
                </p>
              )}
            </div>
          );
        })}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

function BackupDisks({ disks }: { disks: DataDisk[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Backup disks</CardTitle>
        <CardDescription>
          To keep backups on one, add a folder destination with its path on the node's Backups tab.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {disks.map((x) => (
          <div key={x.id} className="flex flex-col gap-1.5">
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="font-medium">{diskName(x)}</span>
              <span className="text-muted-foreground">{diskSpace(x)}</span>
            </div>
            <UsageBar fraction={x.total ? (x.total - x.free) / x.total : 0} />
            <p className="font-mono text-xs text-muted-foreground">{x.mountpoint}</p>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function Devices({
  devices,
  onSetup,
}: {
  devices: BlockDevice[];
  onSetup: (d: BlockDevice) => void;
}) {
  const rows: { d: BlockDevice; depth: number }[] = [];
  const walk = (list: BlockDevice[], depth: number) => {
    for (const d of list) {
      rows.push({ d, depth });
      walk(d.children ?? [], depth + 1);
    }
  };
  walk(devices, 0);
  const empty = rows.some((r) => r.d.use === "empty");
  return (
    <Card>
      <CardHeader>
        <CardTitle>Disks on this machine</CardTitle>
        <CardDescription>
          {empty
            ? "An empty disk can be set up for server data or backups: Raptor formats it and mounts it, at boot too."
            : "Raptor only sets up empty disks: no partitions, filesystem, or data. To reuse one that has something on it, wipe it over SSH first (sudo wipefs -a /dev/…), if you're sure."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Disk</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Used for</TableHead>
              <TableHead className="hidden md:table-cell">Mounted at</TableHead>
              <TableHead className="w-28">
                <span className="sr-only">Set up</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map(({ d, depth }) => (
              <TableRow key={d.path}>
                <TableCell>
                  <span className="flex flex-col" style={{ paddingLeft: depth * 16 }}>
                    <span className="font-mono text-xs">{d.path}</span>
                    {d.model && <span className="text-xs text-muted-foreground">{d.model}</span>}
                  </span>
                </TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(d.size)}</TableCell>
                <TableCell>
                  <Badge variant={d.use === "empty" ? "default" : "outline"}>
                    {useLabels[d.use] ?? d.use}
                  </Badge>
                  {d.fstype && (
                    <span className="ml-1.5 text-xs text-muted-foreground">{d.fstype}</span>
                  )}
                </TableCell>
                <TableCell className="hidden font-mono text-xs text-muted-foreground md:table-cell">
                  {(d.mountpoints ?? []).join(", ") || "—"}
                </TableCell>
                <TableCell className="text-right">
                  {d.use === "empty" && (
                    <Button size="sm" variant="outline" onClick={() => onSetup(d)}>
                      Set up
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function SetupDialog({
  nodeId,
  userId,
  device,
  onClose,
}: {
  nodeId: string;
  userId: string;
  device: BlockDevice;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [role, setRole] = useState<"servers" | "backups">("servers");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await sendSigned({
        userId,
        nodeId,
        action: "node.disk.setup",
        params: {
          device: device.path,
          role,
          size: device.size,
          serial: device.serial || undefined,
        },
      });
      const id = (JSON.parse(res.resultJson || "{}") as { disk?: string }).disk ?? "";
      setDone(id);
      await client.invalidateQueries({ queryKey: ["node-disks", nodeId] });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Set up {device.path}</DialogTitle>
          <DialogDescription>
            {formatBytes(device.size)}
            {device.model ? `, ${device.model}` : ""}. Raptor formats it XFS and mounts it, at boot
            too. It's empty now; anything on it would be gone.
          </DialogDescription>
        </DialogHeader>
        {done ? (
          <div className="flex flex-col gap-3 text-sm">
            <p className="flex items-center gap-2">
              <CheckCircle2 className="size-4 text-emerald-500" /> {device.path} is set up as disk{" "}
              {done}.
            </p>
            <p className="text-muted-foreground">
              {role === "servers"
                ? "Pick it when creating a server, or make it the default for new servers above."
                : "Add a folder destination with its path on the node's Backups tab to keep backups there."}
            </p>
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1 text-sm font-medium">Use it for</legend>
              {(
                [
                  [
                    "servers",
                    "Server data",
                    "Servers' files, at full disk speed with disk limits.",
                  ],
                  [
                    "backups",
                    "Backups",
                    "A folder for backups, so they're on a different disk from the servers.",
                  ],
                ] as const
              ).map(([value, title, text]) => (
                <label
                  key={value}
                  className={cn(
                    "flex cursor-pointer gap-3 rounded-lg border p-3 text-sm",
                    role === value && "border-primary",
                  )}
                >
                  <input
                    type="radio"
                    name="role"
                    className="mt-0.5"
                    checked={role === value}
                    onChange={() => setRole(value)}
                  />
                  <span>
                    <span className="font-medium">{title}</span>
                    <span className="block text-muted-foreground">{text}</span>
                  </span>
                </label>
              ))}
            </fieldset>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="disk-confirm">
                Type <code>{device.name}</code> to confirm
              </Label>
              <Input
                id="disk-confirm"
                autoComplete="off"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy || typed.trim() !== device.name}>
                {busy && <Loader2 className="animate-spin" />} Format with passkey
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
