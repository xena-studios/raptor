import { useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import {
  ArrowUpCircle,
  Database as DatabaseIcon,
  Globe,
  History,
  Loader2,
  MoreHorizontal,
  Pencil,
  Play,
  Plus,
  RotateCw,
  Square,
  Trash2,
} from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { ExternalDialog } from "@/components/db-external";
import { StatusBadge } from "@/components/server-status";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  type DBInstance,
  type DBVersion,
  defaultMaintenance,
  type Engine,
  engineName,
  engines,
  type InstanceInput,
  instancesKey,
  isSQL,
  useInstances,
  versionChange,
} from "@/lib/databases";
import { message } from "@/lib/errors";
import { defaultNetworkName, type RaptorNetwork, useNodeNetworks } from "@/lib/networks";
import type { ServerStatus } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// The node's Databases tab (docs/DATABASES.md): database instances Wings
// runs on the machine, which servers on their networks get databases on.
// A new instance takes an engine, a name, and its memory; the rest is
// under Advanced.
export function NodeDatabases({
  nodeId,
  userId,
  connected,
  supported,
  networksSupported,
  versionsSupported,
  externalSupported,
}: {
  nodeId: string;
  userId: string;
  connected: boolean;
  supported: boolean;
  networksSupported: boolean;
  // The node's Wings changes instances' versions.
  versionsSupported: boolean;
  // The node's Wings gives instances external access.
  externalSupported: boolean;
}) {
  const insts = useInstances(nodeId, connected && supported);
  const nets = useNodeNetworks(nodeId, connected && networksSupported);
  const [editing, setEditing] = useState<DBInstance | "new">();
  const [changing, setChanging] = useState<DBInstance>();
  const [opening, setOpening] = useState<DBInstance>();

  if (!supported) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          Update Wings on this node to run databases on it.
        </CardContent>
      </Card>
    );
  }
  if (!connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          The node is offline, so its databases can't be read until it's back.
        </CardContent>
      </Card>
    );
  }
  if (insts.error) return <p className="text-sm text-destructive">{message(insts.error)}</p>;
  const d = insts.data;
  if (!d) return <p className="text-sm text-muted-foreground">Asking the node…</p>;
  const networks = nets.data?.networks ?? [];

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Database instances</CardTitle>
          <CardDescription>
            Database servers on this machine. Servers on the same network get their own database and
            user on one from their Databases tab. Included in the node's price.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setEditing("new")}>
              <Plus /> New instance
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {d.instances.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No instances yet. Most Minecraft plugins want MariaDB; one instance serves every
              server on its network.
            </p>
          ) : (
            d.instances.map((i) => (
              <InstanceRow
                key={i.id}
                nodeId={nodeId}
                userId={userId}
                instance={i}
                networks={networks}
                versions={versionsSupported}
                onEdit={() => setEditing(i)}
                onChangeVersion={() => setChanging(i)}
                onExternal={externalSupported && isSQL(i.engine) ? () => setOpening(i) : undefined}
              />
            ))
          )}
        </CardContent>
      </Card>
      {editing && (
        <InstanceDialog
          nodeId={nodeId}
          instance={editing === "new" ? undefined : editing}
          versions={d.versions}
          networks={networks}
          onClose={() => setEditing(undefined)}
        />
      )}
      {opening && (
        <ExternalDialog
          nodeId={nodeId}
          userId={userId}
          instance={opening}
          onClose={() => setOpening(undefined)}
        />
      )}
      {changing && (
        <VersionDialog
          nodeId={nodeId}
          instance={changing}
          versions={d.versions}
          onClose={() => setChanging(undefined)}
        />
      )}
    </div>
  );
}

const states: Record<DBInstance["state"], ServerStatus> = {
  running: { label: "Running", tone: "live" },
  starting: { label: "Starting", tone: "pending" },
  stopped: { label: "Stopped", tone: "stopped" },
  failed: { label: "Failed", tone: "failed" },
  upgrading: { label: "Changing version", tone: "pending" },
};

function InstanceRow({
  nodeId,
  userId,
  instance: i,
  networks,
  versions,
  onEdit,
  onChangeVersion,
  onExternal,
}: {
  nodeId: string;
  userId: string;
  instance: DBInstance;
  networks: RaptorNetwork[];
  versions: boolean;
  onEdit: () => void;
  onChangeVersion: () => void;
  onExternal?: () => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const onNames = i.networks.length
    ? i.networks.map((id) => networks.find((n) => n.id === id)?.name ?? "a deleted network")
    : [defaultNetworkName];

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const power = (action: "start" | "stop" | "restart") =>
    act(() =>
      commandClient.execute({
        nodeId,
        action: "node.db.instance.power",
        paramsJson: JSON.stringify({ id: i.id, action }),
      }),
    );

  const changing = i.change && !i.change.done;

  async function update() {
    const ok = await ask.confirm({
      title: `Update ${i.name}?`,
      description: `It restarts on the newest ${engineName(i.engine)} ${i.version} patch, so servers using it reconnect within a minute or so. If it doesn't come up as it was, it goes back by itself. Otherwise this happens in its maintenance window.`,
      confirm: "Update now",
    });
    if (!ok) return;
    await act(() =>
      commandClient.execute({
        nodeId,
        action: "node.db.instance.version",
        paramsJson: JSON.stringify({ id: i.id }),
      }),
    );
  }

  async function goBack() {
    const ok = await ask.confirm({
      title: `Go back to ${engineName(i.engine)} ${i.previous}?`,
      description: `Its data goes back to how it was just before the change to ${i.version}: anything written to its databases since is lost. Servers using it reconnect once it's back.`,
      confirm: "Go back with passkey",
      destructive: true,
    });
    if (!ok) return;
    await act(() =>
      sendSigned({ userId, nodeId, action: "node.db.instance.goback", params: { id: i.id } }),
    );
  }

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${i.name}?`,
      description:
        i.databases > 0
          ? `Its ${i.databases} ${i.databases === 1 ? "database is" : "databases are"} deleted with it, with everything in them. Servers using them lose their data.`
          : "The instance and its files are removed from the machine.",
      typeToConfirm: i.name,
      confirm: "Delete with passkey",
      destructive: true,
    });
    if (!ok) return;
    await act(() =>
      sendSigned({ userId, nodeId, action: "node.db.instance.delete", params: { id: i.id } }),
    );
  }

  return (
    <div className="flex flex-col gap-2 rounded-lg border p-3">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2 py-0.5">
          <DatabaseIcon className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate font-medium">{i.name}</span>
          <Badge variant="secondary">
            {engineName(i.engine)} {i.version}
          </Badge>
          <StatusBadge status={states[i.state]} />
          {versions && i.update && !changing && (
            <Badge variant="outline" className="text-primary">
              Update available
            </Badge>
          )}
        </div>
        <div className="-my-0.5 flex shrink-0 gap-1">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Edit ${i.name}`}
            disabled={changing}
            onClick={onEdit}
          >
            <Pencil />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Actions for ${i.name}`}
                  disabled={busy || changing}
                />
              }
            >
              {busy ? <Loader2 className="animate-spin" /> : <MoreHorizontal />}
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              {i.desired_state === "stopped" ? (
                <DropdownMenuItem onClick={() => power("start")}>
                  <Play /> Start
                </DropdownMenuItem>
              ) : (
                <>
                  <DropdownMenuItem onClick={() => power("restart")}>
                    <RotateCw /> Restart
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => power("stop")}>
                    <Square /> Stop
                  </DropdownMenuItem>
                </>
              )}
              {onExternal && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={onExternal}>
                    <Globe /> External access
                  </DropdownMenuItem>
                </>
              )}
              {versions && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={onChangeVersion}>
                    <ArrowUpCircle /> Change version
                  </DropdownMenuItem>
                  {i.update && (
                    <DropdownMenuItem onClick={update}>
                      <RotateCw /> Update to the newest patch
                    </DropdownMenuItem>
                  )}
                  {i.previous && (
                    <DropdownMenuItem onClick={goBack}>
                      <History /> Go back to {i.previous}
                    </DropdownMenuItem>
                  )}
                </>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onClick={remove}>
                <Trash2 /> Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        <span className="font-mono">
          {i.host}:{i.port}
        </span>{" "}
        · on {onNames.join(", ")} · {formatMemory(i.limits.memory_mib)} memory · {i.databases}{" "}
        {i.databases === 1 ? "database" : "databases"}
      </p>
      {i.state === "failed" && i.error && (
        <p className="text-sm text-destructive">It didn't start: {i.error}</p>
      )}
      {i.external && (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Globe className="size-3.5 shrink-0" />
          <span>
            Reachable from outside on port{" "}
            <span className="font-mono text-foreground">{i.external.port}</span>, by{" "}
            {i.external.allow.length === 1
              ? i.external.allow[0]
              : `${i.external.allow.length} addresses`}
            , with TLS
          </span>
        </p>
      )}
      {i.change && <ChangeLine instance={i} />}
      {i.previous && i.go_back_until && !changing && (
        <p className="text-xs text-muted-foreground">
          Changed from {engineName(i.engine)} {i.previous}. Until{" "}
          {new Date(i.go_back_until * 1000).toLocaleDateString(undefined, {
            month: "short",
            day: "numeric",
          })}{" "}
          it can go back to it, with its data from just before the change.
        </p>
      )}
      {i.state === "starting" && i.databases === 0 && (
        <p className="text-xs text-muted-foreground">
          The first start downloads the engine and sets it up, which takes a minute or two.
        </p>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  );
}

// ChangeLine is how a version change is going, or how the last one went.
function ChangeLine({ instance: i }: { instance: DBInstance }) {
  const c = i.change;
  if (!c) return null;
  const to = `${engineName(i.engine)} ${c.to}`;
  if (!c.done) {
    const what =
      c.kind === "back"
        ? `Going back to ${to}`
        : c.kind === "patch"
          ? "Updating"
          : `Changing to ${to}`;
    return (
      <p className="flex items-center gap-2 text-sm" role="status">
        <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
        <span>
          {what}
          {c.step ? <span className="text-muted-foreground"> · {c.step}</span> : null}
        </span>
      </p>
    );
  }
  if (c.error) {
    return (
      <p className="text-sm text-destructive">
        {c.kind === "back" ? `Going back to ${to}` : `The change to ${to}`} didn't work: {c.error}
      </p>
    );
  }
  return null;
}

function formatMemory(mib: number): string {
  return mib >= 1024 ? `${+(mib / 1024).toFixed(1)} GB` : `${mib} MB`;
}

// InstanceDialog makes an instance or changes one. The engine, a name, and
// memory are all a new one needs; Advanced has the rest.
function InstanceDialog({
  nodeId,
  instance,
  versions,
  networks,
  onClose,
}: {
  nodeId: string;
  instance?: DBInstance;
  versions: DBVersion[];
  networks: RaptorNetwork[];
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [engine, setEngine] = useState<Engine>(instance?.engine ?? "mariadb");
  const [version, setVersion] = useState(instance?.version ?? "");
  const [name, setName] = useState(instance?.name ?? "");
  const [network, setNetwork] = useState(instance?.networks[0] ?? "");
  const [extra, setExtra] = useState<string[]>(instance?.networks.slice(1) ?? []);
  const [memoryGiB, setMemoryGiB] = useState(String((instance?.limits.memory_mib ?? 1024) / 1024));
  const [cpu, setCpu] = useState(
    instance?.limits.cpu_percent ? String(instance.limits.cpu_percent) : "",
  );
  const [diskGiB, setDiskGiB] = useState(
    instance?.limits.disk_mib ? String(instance.limits.disk_mib / 1024) : "",
  );
  const [maxConn, setMaxConn] = useState(
    instance?.settings.max_connections ? String(instance.settings.max_connections) : "",
  );
  const [timezone, setTimezone] = useState(instance?.settings.timezone ?? "");
  const [maintenance, setMaintenance] = useState(instance?.settings.maintenance ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const engineVersions = versions.filter((v) => v.engine === engine);
  const shownVersion = version || engineVersions.find((v) => v.default)?.version || "";
  const placeholder = `${engineName(engine)}${network ? ` for ${networks.find((n) => n.id === network)?.name}` : ""}`;

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const input: InstanceInput = {
      id: instance?.id,
      name: name.trim() || placeholder,
      engine: instance ? undefined : engine,
      version: instance ? undefined : shownVersion,
      networks: network ? [network, ...extra.filter((x) => x !== network)] : [],
      limits: {
        memory_mib: Math.round(Number(memoryGiB) * 1024),
        cpu_percent: cpu ? Number(cpu) : undefined,
        disk_mib: diskGiB ? Math.round(Number(diskGiB) * 1024) : undefined,
      },
      settings: {
        max_connections: maxConn ? Number(maxConn) : undefined,
        timezone: timezone.trim() || undefined,
        maintenance: maintenance || undefined,
      },
    };
    try {
      await commandClient.execute({
        nodeId,
        action: "node.db.instance.save",
        paramsJson: JSON.stringify(input),
      });
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              {instance ? `Edit ${instance.name}` : "New database instance"}
            </DialogTitle>
            <DialogDescription>
              {instance
                ? "Changes restart it, so servers using it reconnect."
                : "It runs on this machine, and servers on its network can have databases on it."}
            </DialogDescription>
          </DialogHeader>

          {!instance && (
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
                Engine
              </legend>
              <div className="grid gap-2 sm:grid-cols-3">
                {engines
                  .filter((x) => versions.some((v) => v.engine === x.id))
                  .map((x) => (
                    <label
                      key={x.id}
                      className={cn(
                        "flex cursor-pointer flex-col gap-1 rounded-lg border p-3 transition-colors hover:bg-muted/50 has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                        engine === x.id && "border-primary bg-brand-wash hover:bg-brand-wash",
                      )}
                    >
                      <input
                        type="radio"
                        name="engine"
                        className="sr-only"
                        checked={engine === x.id}
                        onChange={() => {
                          setEngine(x.id);
                          setVersion("");
                        }}
                      />
                      <span className="text-sm font-medium">{x.name}</span>
                      <span className="text-xs text-muted-foreground">{x.about}</span>
                    </label>
                  ))}
              </div>
            </fieldset>
          )}

          <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="instance-name">Name</Label>
              <Input
                id="instance-name"
                maxLength={40}
                placeholder={placeholder}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="instance-memory">Memory (GB)</Label>
              <Input
                id="instance-memory"
                type="number"
                min={0.25}
                step={0.25}
                required
                value={memoryGiB}
                onChange={(e) => setMemoryGiB(e.target.value)}
              />
            </div>
          </div>

          {networks.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="instance-network">Network</Label>
              <select
                id="instance-network"
                className={select}
                value={network}
                onChange={(e) => setNetwork(e.target.value)}
              >
                <option value="">{defaultNetworkName}</option>
                {networks.map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Only servers on this network can have databases on it, or reach it at all.
              </p>
            </div>
          )}

          <details className="rounded-lg border p-3">
            <summary className="cursor-pointer text-sm font-medium">Advanced</summary>
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              {!instance && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="instance-version">Version</Label>
                  <select
                    id="instance-version"
                    className={select}
                    value={shownVersion}
                    onChange={(e) => setVersion(e.target.value)}
                  >
                    {engineVersions.map((v) => (
                      <option key={v.version} value={v.version}>
                        {engineName(v.engine)} {v.version}
                        {v.default ? " (recommended)" : ""}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <Field id="instance-maxconn" label="Max connections">
                <Input
                  id="instance-maxconn"
                  type="number"
                  min={10}
                  max={2000}
                  placeholder="100"
                  value={maxConn}
                  onChange={(e) => setMaxConn(e.target.value)}
                />
              </Field>
              <Field id="instance-cpu" label="CPU limit (%)">
                <Input
                  id="instance-cpu"
                  type="number"
                  min={10}
                  placeholder="No limit"
                  value={cpu}
                  onChange={(e) => setCpu(e.target.value)}
                />
              </Field>
              <Field id="instance-disk" label="Disk limit (GB)">
                <Input
                  id="instance-disk"
                  type="number"
                  min={0.5}
                  step="any"
                  placeholder="No limit"
                  value={diskGiB}
                  onChange={(e) => setDiskGiB(e.target.value)}
                />
              </Field>
              <Field id="instance-tz" label="Time zone">
                <Input
                  id="instance-tz"
                  placeholder="The node's"
                  spellCheck={false}
                  value={timezone}
                  onChange={(e) => setTimezone(e.target.value)}
                />
              </Field>
              <Field id="instance-maintenance" label="Patch updates">
                <select
                  id="instance-maintenance"
                  className={select}
                  value={maintenance}
                  onChange={(e) => setMaintenance(e.target.value)}
                >
                  <option value="">At {defaultMaintenance} (the default)</option>
                  {hours
                    .filter((h) => h !== defaultMaintenance)
                    .map((h) => (
                      <option key={h} value={h}>
                        At {h}
                      </option>
                    ))}
                  <option value="off">Only when I say</option>
                </select>
                <p className="text-xs text-muted-foreground">
                  When a newer patch restarts it, in the node's time.
                </p>
              </Field>
              {network && networks.length > 1 && (
                <fieldset className="flex flex-col gap-2 sm:col-span-2">
                  <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
                    Also on
                  </legend>
                  {networks
                    .filter((n) => n.id !== network)
                    .map((n) => (
                      <label key={n.id} className="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          className="accent-primary"
                          checked={extra.includes(n.id)}
                          onChange={(e) =>
                            setExtra(
                              e.target.checked ? [...extra, n.id] : extra.filter((x) => x !== n.id),
                            )
                          }
                        />
                        {n.name}
                      </label>
                    ))}
                </fieldset>
              )}
            </div>
          </details>

          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              {instance ? "Save" : "Create instance"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const hours = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, "0")}:00`);

// VersionDialog moves an instance to another version. Wings copies its
// data first, checks every table after, and puts it back if anything's
// off; older versions aren't offered, since they can't read newer data.
function VersionDialog({
  nodeId,
  instance: i,
  versions,
  onClose,
}: {
  nodeId: string;
  instance: DBInstance;
  versions: DBVersion[];
  onClose: () => void;
}) {
  const client = useQueryClient();
  const newer = versions.filter(
    (v) =>
      v.engine === i.engine &&
      v.version !== i.version &&
      !["older", "skip"].includes(versionChange(i.engine, i.version, v.version)),
  );
  const [to, setTo] = useState(newer[newer.length - 1]?.version ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const how = to ? versionChange(i.engine, i.version, to) : undefined;
  const engine = engineName(i.engine);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "node.db.instance.version",
        paramsJson: JSON.stringify({ id: i.id, version: to }),
      });
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Change {i.name}'s version</DialogTitle>
            <DialogDescription>
              It's on {engine} {i.version}.
            </DialogDescription>
          </DialogHeader>
          {newer.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              It's on the newest version this node offers. Newer ones come with Wings updates.
            </p>
          ) : (
            <>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="version-to">New version</Label>
                <select
                  id="version-to"
                  className={select}
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                >
                  {newer.map((v) => (
                    <option key={v.version} value={v.version}>
                      {engine} {v.version}
                      {v.default ? " (recommended)" : ""}
                    </option>
                  ))}
                </select>
              </div>
              <ul className="flex list-disc flex-col gap-1.5 pl-5 text-sm text-muted-foreground">
                {how === "migrate" ? (
                  <li>
                    {engine} {to} can't use {i.version}'s data files, so Wings dumps every database,
                    starts {to} on a new data folder, and restores them into it.
                  </li>
                ) : (
                  <li>
                    Wings copies its data, then starts {engine} {to} on it
                    {i.engine === "mariadb" ? " and upgrades its system tables" : ""}.
                  </li>
                )}
                <li>
                  Every table's rows are counted before and after. If {to} doesn't start or anything
                  differs, it's put back on {i.version} as it was, by itself.
                </li>
                <li>
                  {i.databases > 0
                    ? `Servers using its ${i.databases} ${i.databases === 1 ? "database" : "databases"} lose their connection while it switches: a minute or so, longer for big databases.`
                    : "Nothing uses it yet, so nothing notices."}
                </li>
                <li>
                  For 7 days you can go back to {i.version}, with the data from just before the
                  change. After that there's no going down a version: older versions can't read
                  newer data.
                  {i.previous ? ` This replaces the chance to go back to ${i.previous}.` : ""}
                </li>
              </ul>
            </>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            {newer.length > 0 && (
              <Button type="submit" disabled={busy || !to}>
                {busy && <Loader2 className="animate-spin" />}
                Change to {to}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}
