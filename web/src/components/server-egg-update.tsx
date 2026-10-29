import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, PackageCheck } from "lucide-react";
import { useState } from "react";

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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { eggUpdate, eggVersionInfo, type ServerConfig, updateEggParams } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

// useEggVersions is the library egg a server was made from, its versions,
// and which one the server runs (by its file's hash). Undefined while
// loading, or when the egg isn't in the library or isn't published.
export function useEggVersions(orgId: string, eggSource: string, cfg: ServerConfig | undefined) {
  const details = useQuery(
    EggService.method.getEggDetails,
    { orgId, id: eggSource },
    { enabled: !!eggSource && !!cfg?.egg_hash, retry: false },
  );
  const egg = details.data?.egg;
  if (!cfg?.egg_hash || !egg || egg.status !== "published" || !details.data) return undefined;
  const versions = [...details.data.versions].sort((a, b) => b.version - a.version);
  return { egg, versions, ...eggVersionInfo(cfg.egg_hash, egg.version, versions) };
}

// ServerEggVersion shows which version of its library egg a server runs,
// and moves it to another one, newer or older (docs/SERVERS.md). Servers
// keep their own copy of their egg, so a library edit never changes one
// quietly; this is the explicit change: a passkey-signed server.update with
// that version's file, installing it again only if asked.
export function ServerEggVersion({
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
  const cfg = server.data?.configJson
    ? (JSON.parse(server.data.configJson) as ServerConfig)
    : undefined;
  const v = useEggVersions(orgId, eggSource, cfg);
  const [picked, setPicked] = useState<number>();
  const [open, setOpen] = useState(false);
  if (!cfg || !v) return null;
  const target = picked ?? (v.outdated ? v.latest : v.current) ?? v.latest;
  const older = v.current !== undefined && target < v.current;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <PackageCheck className="size-4 text-muted-foreground" />
          Egg version
          {v.outdated ? (
            <Badge variant="outline">Update available</Badge>
          ) : (
            <Badge variant="secondary">Newest</Badge>
          )}
        </CardTitle>
        <CardDescription>
          {v.current !== undefined
            ? `It runs version ${v.current} of ${v.egg.name}${v.outdated ? `; the newest is version ${v.latest}` : ", the newest"}.`
            : `Its copy of ${v.egg.name} isn't one of the library's versions (it was edited, or made before the library kept versions).`}{" "}
          Servers keep the egg they have until you change it here, or when you reinstall.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="egg-version">Version</Label>
          <select
            id="egg-version"
            className="h-8 min-w-64 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
            value={target}
            onChange={(e) => setPicked(Number(e.target.value))}
          >
            {v.versions.map((x) => (
              <option key={x.version} value={x.version}>
                {[
                  `Version ${x.version}`,
                  x.version === v.latest ? "newest" : "",
                  x.version === v.current ? "in use" : "",
                  x.note,
                  x.createdAt ? dateOnly(timestampDate(x.createdAt)) : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </option>
            ))}
          </select>
        </div>
        <Button
          variant={v.outdated && target === v.latest ? "default" : "outline"}
          disabled={target === v.current}
          onClick={() => setOpen(true)}
        >
          {older ? `Go back to version ${target}` : `Update to version ${target}`}
        </Button>
      </CardContent>
      {open && (
        <EggVersionDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          eggSource={eggSource}
          name={server.data?.server?.name ?? ""}
          cfg={cfg}
          current={v.current}
          target={target}
          eggName={v.egg.name}
          stopped={stopped}
          onClose={() => setOpen(false)}
        />
      )}
    </Card>
  );
}

// EggVersionDialog moves a server to one version of its egg: what changes,
// values for new settings it needs, and whether to reinstall. reinstall
// set (from the Reinstall card) makes it a reinstall that updates too.
export function EggVersionDialog({
  orgId,
  nodeId,
  serverId,
  userId,
  eggSource,
  name,
  cfg,
  current,
  target,
  eggName,
  stopped,
  reinstall: preset,
  onClose,
  onDone,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  eggSource: string;
  name: string;
  cfg: ServerConfig;
  // The library version the server has; undefined if it isn't one.
  current: number | undefined;
  target: number;
  eggName: string;
  stopped: boolean;
  reinstall?: { wipe: boolean };
  onClose: () => void;
  onDone?: () => void;
}) {
  const client = useQueryClient();
  const old = useQuery(
    EggService.method.getEggDetails,
    { orgId, id: eggSource, version: current ?? 0 },
    { enabled: current !== undefined, retry: false },
  );
  const next = useQuery(
    EggService.method.getEggDetails,
    { orgId, id: eggSource, version: target },
    { retry: false },
  );
  const [values, setValues] = useState<Record<string, string>>({});
  const [reinstall, setReinstall] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const spec = next.data?.spec;
  const file = next.data?.file;
  const loading = (current !== undefined && old.isPending) || next.isPending;
  const older = current !== undefined && target < current;
  const u = spec
    ? eggUpdate(cfg, current !== undefined ? old.data?.spec : undefined, spec)
    : undefined;
  if (next.error || (!loading && (!u || !file))) {
    return (
      <Dialog open onOpenChange={(o) => !o && onClose()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Version {target} can't be loaded</DialogTitle>
            <DialogDescription>{next.error ? message(next.error) : "Try again."}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }
  if (!u || !file) {
    return (
      <Dialog open onOpenChange={(o) => !o && onClose()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Version {target}</DialogTitle>
            <DialogDescription>Comparing versions…</DialogDescription>
          </DialogHeader>
        </DialogContent>
      </Dialog>
    );
  }
  const eggFile = file;
  const plan = u;
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
          { source: eggSource, file: eggFile },
          plan,
          values,
          preset ?? (reinstall ? { wipe: false } : undefined),
        ),
      });
      await client.invalidateQueries();
      onDone?.();
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
          <DialogTitle>
            {preset
              ? `${preset.wipe ? "Wipe and reinstall" : "Reinstall"} with version ${target}?`
              : older
                ? `Go back to version ${target}?`
                : `Update to version ${target}?`}
          </DialogTitle>
          <DialogDescription>
            {preset
              ? preset.wipe
                ? `Every file is deleted, after a safety backup, then it installs clean with version ${target} of ${eggName}.`
                : `A backup of its files is taken, then version ${target} of ${eggName} installs over them.`
              : `This server gets version ${target} of ${eggName}. Its files and settings stay, and the change applies the next time it starts.`}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          {loading ? (
            <p className="text-sm text-muted-foreground">Comparing versions…</p>
          ) : (
            <div className="flex flex-col gap-1.5">
              <p className="text-sm font-medium">What changes</p>
              {current === undefined && (
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
          {!preset && (
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
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || loading || missing.length > 0} onClick={update}>
            {busy && <Loader2 className="animate-spin" />}
            {preset ? "Reinstall with passkey" : "Update with passkey"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
