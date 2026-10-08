import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { changesCode, type ServerConfig, updateParams } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// ServerSettings changes a server's name, runtime, variables, memory, disk
// limit, and port (server.update; a new runtime is signed by the user's
// passkey), and reinstalls it.
export function ServerSettings({
  orgId,
  nodeId,
  serverId,
  userId,
  admin,
  canReinstall,
  stopped,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  admin: boolean;
  canReinstall: boolean;
  stopped: boolean;
}) {
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  if (res.error) return <p className="text-sm text-destructive">{message(res.error)}</p>;
  if (!res.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div className="flex flex-col gap-4">
      {cfg ? (
        <SettingsForm
          key={res.data.configJson}
          name={res.data.server?.name ?? ""}
          cfg={cfg}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          admin={admin}
        />
      ) : (
        <p className="text-sm text-muted-foreground">
          You don't have access to this server's settings.
        </p>
      )}
      {canReinstall && (
        <Reinstall
          stopped={stopped}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          name={res.data.server?.name ?? ""}
        />
      )}
    </div>
  );
}

function SettingsForm({
  name: initialName,
  cfg,
  nodeId,
  serverId,
  userId,
  admin,
}: {
  name: string;
  cfg: ServerConfig;
  nodeId: string;
  serverId: string;
  userId: string;
  admin: boolean;
}) {
  const client = useQueryClient();
  const images = cfg.egg?.images ?? [];
  // Members see and change what the egg lets them; admins and owners
  // everything.
  const vars = (cfg.egg?.variables ?? []).filter(
    (v) => admin || v.user_viewable || v.user_editable,
  );
  const primary = cfg.allocations.find((a) => a.primary) ?? cfg.allocations[0];
  const [name, setName] = useState(initialName);
  const [image, setImage] = useState(cfg.image);
  const [values, setValues] = useState<Record<string, string>>(() => {
    const out: Record<string, string> = {};
    for (const v of vars) out[v.env] = cfg.variables?.[v.env] ?? v.default;
    return out;
  });
  const [memoryGiB, setMemoryGiB] = useState(cfg.limits.memory_mib / 1024);
  const [diskGiB, setDiskGiB] = useState(
    cfg.limits.disk_mib ? String(cfg.limits.disk_mib / 1024) : "",
  );
  const [port, setPort] = useState(primary?.port ?? 25565);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const change = {
    name,
    image,
    variables: values,
    memoryMiB: Math.round(memoryGiB * 1024),
    diskMiB: diskGiB ? Math.round(Number(diskGiB) * 1024) : 0,
    port,
  };
  const signed = changesCode(cfg, change);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const params = updateParams(cfg, change);
      if (signed) {
        await sendSigned({ userId, nodeId, action: "server.update", serverId, params });
      } else {
        await commandClient.execute({
          nodeId,
          action: "server.update",
          serverId,
          paramsJson: JSON.stringify(params),
        });
      }
      setSaved(true);
      await client.invalidateQueries();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={save}>
      <Card>
        <CardHeader>
          <CardTitle>Settings</CardTitle>
          <CardDescription>
            Changes apply the next time the server starts; the disk limit applies at once.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="s-name">Name</Label>
            <Input
              id="s-name"
              required
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          {images.length > 1 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="s-image">Runtime</Label>
              <select
                id="s-image"
                className={select}
                value={image}
                onChange={(e) => setImage(e.target.value)}
              >
                {images.map((i) => (
                  <option key={i.ref} value={i.ref}>
                    {i.name}
                  </option>
                ))}
              </select>
              {signed && (
                <p className="text-xs text-muted-foreground">
                  Changing the runtime changes what code runs, so your passkey signs it.
                </p>
              )}
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="s-port">Port</Label>
              <Input
                id="s-port"
                type="number"
                required
                min={1024}
                max={65535}
                value={port}
                onChange={(e) => setPort(Number(e.target.value))}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="s-memory">Memory (GB)</Label>
              <Input
                id="s-memory"
                type="number"
                required
                min={0.0625}
                step="any"
                value={memoryGiB}
                onChange={(e) => setMemoryGiB(Number(e.target.value))}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="s-disk">Disk limit (GB)</Label>
              <Input
                id="s-disk"
                type="number"
                min={1}
                placeholder="No limit"
                value={diskGiB}
                onChange={(e) => setDiskGiB(e.target.value)}
              />
            </div>
          </div>
          {vars.map((v) => (
            <div key={v.env} className="flex flex-col gap-1.5">
              <Label htmlFor={`s-var-${v.env}`}>{v.name}</Label>
              <Input
                id={`s-var-${v.env}`}
                value={values[v.env] ?? ""}
                placeholder={v.default}
                disabled={!admin && !v.user_editable}
                onChange={(e) => setValues({ ...values, [v.env]: e.target.value })}
              />
              {v.description && <p className="text-xs text-muted-foreground">{v.description}</p>}
            </div>
          ))}
          {!cfg.egg && (
            <p className="text-xs text-muted-foreground">
              The node hasn't reported this server's game settings yet. Update Wings to edit them
              here.
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={busy}>
              {busy ? "Saving…" : signed ? "Save with a passkey" : "Save"}
            </Button>
            {saved && <span className="text-sm text-muted-foreground">Saved.</span>}
          </div>
        </CardContent>
      </Card>
    </form>
  );
}

function Reinstall({
  stopped,
  nodeId,
  serverId,
  userId,
  name,
}: {
  stopped: boolean;
  nodeId: string;
  serverId: string;
  userId: string;
  name: string;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function reinstall(wipe: boolean) {
    const question = wipe
      ? `Wipe "${name}" and reinstall it? Every file is deleted (a safety backup is taken first). Type its name to confirm.`
      : `Reinstall "${name}"? The install script runs again over its files. Type its name to confirm.`;
    if (window.prompt(question) !== name) return;
    setBusy(true);
    setError("");
    setDone("");
    try {
      if (wipe) {
        // Wiping deletes everything: signed by the user's passkey.
        await sendSigned({
          userId,
          nodeId,
          action: "server.reinstall",
          serverId,
          params: { wipe: true },
        });
      } else {
        await commandClient.execute({ nodeId, action: "server.reinstall", serverId });
      }
      setDone("The reinstall started. The console shows its progress.");
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Reinstall</CardTitle>
        <CardDescription>
          Runs the game's install script again, which fixes a broken or outdated install.
          {!stopped && " Stop the server first."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={busy || !stopped} onClick={() => reinstall(false)}>
            Reinstall
          </Button>
          <Button variant="destructive" disabled={busy || !stopped} onClick={() => reinstall(true)}>
            Wipe and reinstall
          </Button>
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {done && <p className="text-sm text-muted-foreground">{done}</p>}
      </CardContent>
    </Card>
  );
}
