import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanstackQuery } from "@tanstack/react-query";
import { Loader2, PackageCheck } from "lucide-react";
import { useState } from "react";

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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { EggService, type EggSpec } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { eggUpdate, type ServerConfig, sha256Hex, updateEggParams } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

// ServerEggUpdate offers a newer version of the library egg a server was
// made from (docs/SERVERS.md). Servers keep their own copy of their egg, so
// a library edit never changes one quietly; this is the explicit update: a
// passkey-signed server.update with the new file, installing it again over
// the files only if asked.
export function ServerEggUpdate({
  orgId,
  nodeId,
  serverId,
  userId,
  eggSource,
  stopped,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  eggSource: string;
  stopped: boolean;
}) {
  const server = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const details = useQuery(
    EggService.method.getEggDetails,
    { orgId, id: eggSource },
    { enabled: !!eggSource, retry: false },
  );
  const file = details.data?.file;
  const hash = useTanstackQuery({
    queryKey: ["egg-sha256", eggSource, details.data?.egg?.version],
    queryFn: () => sha256Hex(file ?? new Uint8Array()),
    enabled: !!file,
    staleTime: Number.POSITIVE_INFINITY,
  });
  const [open, setOpen] = useState(false);

  const cfg = server.data?.configJson
    ? (JSON.parse(server.data.configJson) as ServerConfig)
    : undefined;
  const egg = details.data?.egg;
  if (!cfg?.egg_hash || !egg || egg.status !== "published" || !hash.data) return null;
  if (hash.data === cfg.egg_hash) return null;
  const has = details.data?.versions.find((v) => v.sha256 === cfg.egg_hash)?.version;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <PackageCheck className="size-4 text-muted-foreground" />
          Egg update
        </CardTitle>
        <CardDescription>
          A newer version of {egg.name} is in your library (version {egg.version}
          {has ? `; this server has version ${has}` : ""}). Servers keep the egg they were made with
          until you update them.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="outline" onClick={() => setOpen(true)}>
          Review the update
        </Button>
      </CardContent>
      {open && details.data?.spec && file && (
        <UpdateDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          eggSource={eggSource}
          name={server.data?.server?.name ?? ""}
          cfg={cfg}
          version={has}
          next={{ spec: details.data.spec, file, version: egg.version, name: egg.name }}
          stopped={stopped}
          onClose={() => setOpen(false)}
        />
      )}
    </Card>
  );
}

function UpdateDialog({
  orgId,
  nodeId,
  serverId,
  userId,
  eggSource,
  name,
  cfg,
  version,
  next,
  stopped,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  eggSource: string;
  name: string;
  cfg: ServerConfig;
  // The library version the server has; undefined if it isn't one.
  version: number | undefined;
  next: {
    spec: EggSpec;
    file: Uint8Array;
    version: number;
    name: string;
  };
  stopped: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const old = useQuery(
    EggService.method.getEggDetails,
    { orgId, id: eggSource, version: version ?? 0 },
    { enabled: version !== undefined, retry: false },
  );
  const [values, setValues] = useState<Record<string, string>>({});
  const [reinstall, setReinstall] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const loading = version !== undefined && old.isPending;
  const u = eggUpdate(cfg, version !== undefined ? old.data?.spec : undefined, next.spec);
  const c = u.changes;
  const missing = u.needed.filter((v) => !(values[v.env] ?? "").trim());
  const lines = [
    ...c.imagesAdded.map((i) => `New runtime: ${i.name}`),
    ...c.imagesRemoved.map((i) => `Runtime removed: ${i.name}`),
    ...(c.image ? [`This server moves to ${c.image.name}, since its runtime is gone`] : []),
    ...(c.startup !== undefined ? ["Its startup command becomes the egg's new one"] : []),
    ...c.variablesAdded.map((v) => `New setting: ${v.name}`),
    ...c.variablesRemoved.map((e) => `Setting removed: ${e}`),
    ...(c.install ? ["The install script changed (it runs at the next reinstall)"] : []),
  ];

  async function update() {
    setBusy(true);
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        serverId,
        action: "server.update",
        params: updateEggParams(
          cfg,
          name,
          { source: eggSource, file: next.file },
          u,
          values,
          reinstall,
        ),
      });
      await client.invalidateQueries();
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Update to version {next.version}?</DialogTitle>
          <DialogDescription>
            This server gets {next.name}'s newest file. Its files and settings stay, and the change
            applies the next time it starts.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          {loading ? (
            <p className="text-sm text-muted-foreground">Comparing versions…</p>
          ) : (
            <div className="flex flex-col gap-1.5">
              <p className="text-sm font-medium">What changes</p>
              {version === undefined && (
                <p className="text-xs text-muted-foreground">
                  This server's egg file isn't one of the library's versions, so only some changes
                  can be shown.
                </p>
              )}
              {lines.length > 0 ? (
                <ul className="list-disc pl-5 text-sm text-muted-foreground">
                  {lines.map((l) => (
                    <li key={l}>{l}</li>
                  ))}
                </ul>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Nothing this server uses: the change is in the egg's other parts (its config
                  files, ready line, or add-ons).
                </p>
              )}
              {c.startup !== undefined && (
                <code className="mt-1 rounded-md bg-muted px-2 py-1 font-mono text-xs break-all">
                  {c.startup}
                </code>
              )}
            </div>
          )}
          {u.needed.map((v) => (
            <div key={v.env} className="grid gap-1.5">
              <Label htmlFor={`egg-update-${v.env}`}>{v.name}</Label>
              <Input
                id={`egg-update-${v.env}`}
                value={values[v.env] ?? ""}
                onChange={(e) => setValues({ ...values, [v.env]: e.target.value })}
              />
              {v.description && <p className="text-xs text-muted-foreground">{v.description}</p>}
            </div>
          ))}
          <div className="flex items-start gap-2">
            <Checkbox
              id="egg-update-reinstall"
              checked={reinstall}
              disabled={!stopped}
              onCheckedChange={(on) => setReinstall(on === true)}
            />
            <div className="grid gap-0.5">
              <Label htmlFor="egg-update-reinstall" className="font-normal">
                Reinstall now
              </Label>
              <p className="text-xs text-muted-foreground">
                {stopped
                  ? "Runs the new install script over its files, after a backup you can restore from. Only needed when the install changed."
                  : "Stop the server to reinstall it now."}
              </p>
            </div>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || loading || missing.length > 0} onClick={update}>
            {busy && <Loader2 className="animate-spin" />}
            Update with passkey
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
