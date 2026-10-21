import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useMemo, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { DetailList } from "@/components/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { shortId } from "@/lib/ids";
import { behaviorOf, changesCode, type ServerConfig, seconds, updateParams } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// A server's settings, split across its tabs: startup (runtime and
// variables), network (its port), and general (name and limits, and
// reinstalling).
type SettingsSection = "startup" | "network" | "general" | "behavior";

const sectionText: Record<SettingsSection, { title: string; description: string }> = {
  startup: {
    title: "Startup",
    description: "The runtime and the game's settings. Changes apply the next time it starts.",
  },
  network: {
    title: "Port",
    description: "The port players connect to. It changes the next time the server starts.",
  },
  general: {
    title: "Name and limits",
    description: "Memory, swap, and CPU apply the next time it starts; the disk limit at once.",
  },
  behavior: {
    title: "Crashes and stopping",
    description: "What the node does when the server crashes or is stopped. Changes apply at once.",
  },
};

// ServerSettings changes one section of a server's settings (server.update;
// a new runtime is signed by the user's passkey), and on the general one
// reinstalls it.
// ServerDetails are the server's facts: its IDs, as support and the
// raptor command on the machine use them.
export function ServerDetails({
  id,
  nodeName,
  game,
}: {
  id: string;
  nodeName: string;
  game: string;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Details</CardTitle>
        <CardDescription>
          About this server. The short ID is what <code>raptor ps</code> on the machine and the
          app's links show.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            [
              "Server ID",
              <code key="id" className="break-all">
                {id}
              </code>,
            ],
            ["Short ID", <code key="short">{shortId(id)}</code>],
            ["Node", nodeName || "—"],
            ["Game", game || "—"],
          ]}
        />
      </CardContent>
    </Card>
  );
}

export function ServerSettings({
  section,
  orgId,
  nodeId,
  serverId,
  userId,
  admin,
  canReinstall,
  stopped,
}: {
  section: SettingsSection;
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
          section={section}
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
      {section === "general" && canReinstall && (
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
  section,
  name: initialName,
  cfg,
  nodeId,
  serverId,
  userId,
  admin,
}: {
  section: SettingsSection;
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
  const [swapGiB, setSwapGiB] = useState(
    cfg.limits.swap_mib ? String(Number(cfg.limits.swap_mib) / 1024) : "",
  );
  const [cpuCores, setCpuCores] = useState(
    cfg.limits.cpu_percent ? String(Number(cfg.limits.cpu_percent) / 100) : "",
  );
  const initial = behaviorOf(cfg);
  const [autoRestart, setAutoRestart] = useState(initial.crash_auto_restart);
  const [cleanExit, setCleanExit] = useState(initial.clean_exit_is_stop);
  const [stopAfter, setStopAfter] = useState(seconds(initial.stop_timeout));
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
    swapMiB: swapGiB ? Math.round(Number(swapGiB) * 1024) : 0,
    cpuPercent: cpuCores ? Math.round(Number(cpuCores) * 100) : 0,
    behavior: {
      crash_auto_restart: autoRestart,
      clean_exit_is_stop: cleanExit,
      stop_timeout: `${stopAfter}s`,
    },
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
          <CardTitle>{sectionText[section].title}</CardTitle>
          <CardDescription>{sectionText[section].description}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {section === "general" && (
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
          )}
          {section === "startup" && cfg.startup && (
            <div className="flex flex-col gap-1.5">
              <Label>Startup command</Label>
              <code className="rounded-lg bg-muted p-3 font-mono text-xs break-all">
                {cfg.startup}
              </code>
            </div>
          )}
          {section === "startup" && images.length > 1 && (
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
          {section === "network" && (
            <div className="flex flex-col gap-1.5 sm:max-w-48">
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
          )}
          {section === "general" && (
            <div className="grid gap-4 sm:grid-cols-2">
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
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="s-swap">Swap (GB)</Label>
                <Input
                  id="s-swap"
                  type="number"
                  min={0}
                  step="any"
                  placeholder="None"
                  value={swapGiB}
                  onChange={(e) => setSwapGiB(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Disk it can use as extra memory, which is much slower. Most games run better
                  without it.
                </p>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="s-cpu">CPU limit (cores)</Label>
                <Input
                  id="s-cpu"
                  type="number"
                  min={0.1}
                  step="any"
                  placeholder="No limit"
                  value={cpuCores}
                  onChange={(e) => setCpuCores(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  The most it can use, in whole or part cores. Without one it shares the machine's
                  CPU fairly with the other servers.
                </p>
              </div>
            </div>
          )}
          {section === "behavior" && (
            <div className="flex flex-col gap-5">
              <div className="flex items-center justify-between gap-4">
                <div className="space-y-0.5">
                  <label htmlFor="s-restart" className="block text-sm font-medium">
                    Restart it when it crashes
                  </label>
                  <p className="text-xs text-muted-foreground">
                    The node starts it again, waiting longer after each crash in a row. Off, it
                    stays crashed until someone starts it.
                  </p>
                </div>
                <Switch id="s-restart" checked={autoRestart} onCheckedChange={setAutoRestart} />
              </div>
              <div className="flex items-center justify-between gap-4">
                <div className="space-y-0.5">
                  <label htmlFor="s-clean" className="block text-sm font-medium">
                    A clean exit means it stopped
                  </label>
                  <p className="text-xs text-muted-foreground">
                    When the game quits without an error, as after a /stop typed in its console,
                    it's left stopped instead of counted as a crash and restarted.
                  </p>
                </div>
                <Switch id="s-clean" checked={cleanExit} onCheckedChange={setCleanExit} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="s-stop">Force stop after (seconds)</Label>
                <Input
                  id="s-stop"
                  type="number"
                  required
                  min={1}
                  max={600}
                  className="sm:max-w-48"
                  value={stopAfter}
                  onChange={(e) => setStopAfter(Number(e.target.value))}
                />
                <p className="text-xs text-muted-foreground">
                  How long stopping waits for the game to save and shut down before the node kills
                  it. Up to 10 minutes; big worlds may need longer than the 60 seconds it starts
                  with.
                </p>
              </div>
            </div>
          )}
          {section === "startup" &&
            vars.map((v) => (
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
          {section === "startup" && !cfg.egg && (
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
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function reinstall(wipe: boolean) {
    const ok = await ask.confirm(
      wipe
        ? {
            title: `Wipe ${name} and reinstall it?`,
            description: "Every file is deleted, after a safety backup, then it installs clean.",
            typeToConfirm: name,
            confirm: "Wipe with passkey",
            destructive: true,
          }
        : {
            title: `Reinstall ${name}?`,
            description: "The install script runs again over its files.",
            typeToConfirm: name,
            confirm: "Reinstall",
          },
    );
    if (!ok) return;
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
