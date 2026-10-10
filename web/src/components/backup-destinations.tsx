import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  CheckCircle2,
  Cloud,
  Copy,
  FolderInput,
  Globe,
  HardDrive,
  KeyRound,
  Loader2,
  type LucideIcon,
  Pencil,
  Plus,
  Server,
  ShieldCheck,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

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
  type Destination,
  type DestinationType,
  formatSpeed,
  presetEndpoint,
  presetFor,
  regionOf,
  s3Presets,
  typeHints,
  typeNames,
} from "@/lib/backup-destinations";
import { message } from "@/lib/errors";
import { formatBytes } from "@/lib/metrics";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";
import { passkeyCancelled } from "@/lib/webauthn";

export const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const typeIcons: Record<DestinationType, LucideIcon> = {
  local: HardDrive,
  folder: FolderInput,
  s3: Cloud,
  azure: Cloud,
  sftp: Server,
  webdav: Globe,
};

// What backup.destinations returns.
export type DestinationList = { destinations: Destination[]; ssh_public_key: string };

export async function nodeCommand<T>(
  nodeId: string,
  action: string,
  params: object = {},
  serverId = "",
): Promise<T> {
  const res = await commandClient.execute({
    nodeId,
    action,
    serverId,
    paramsJson: JSON.stringify(params),
  });
  return (res.resultJson ? JSON.parse(res.resultJson) : {}) as T;
}

export function useDestinations(nodeId: string, enabled = true) {
  return useQuery({
    queryKey: ["backup-destinations", nodeId],
    queryFn: () => nodeCommand<DestinationList>(nodeId, "backup.destinations"),
    enabled,
    refetchInterval: 30_000,
  });
}

// where says where a destination is, in a line.
export function where(d: Destination): string {
  switch (d.type) {
    case "local":
      return "The node's backup folder";
    case "folder":
      return d.folder?.path ?? "";
    case "s3": {
      const s = d.s3;
      if (!s) return "";
      const p = presetFor(s.endpoint);
      return `${s.bucket}${s.prefix ? `/${s.prefix.replace(/\/$/, "")}` : ""} on ${p.id === "custom" ? s.endpoint : p.name}`;
    }
    case "azure":
      return d.azure ? `${d.azure.storage_account} / ${d.azure.container}` : "";
    case "sftp":
      return d.sftp ? `${d.sftp.username}@${d.sftp.host}:${d.sftp.path}` : "";
    case "webdav":
      return d.webdav ? d.webdav.url.replace(/^https?:\/\//, "") : "";
  }
}

function ago(iso?: string): string {
  if (!iso) return "";
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} minutes ago`;
  if (s < 129600) return `${Math.round(s / 3600)} hours ago`;
  return `${Math.round(s / 86400)} days ago`;
}

// Health is how a destination has been doing.
export function Health({ d }: { d: Destination }) {
  const st = d.status;
  const failing =
    st?.last_error &&
    st.last_error_at &&
    (!st.last_ok_at || new Date(st.last_error_at) > new Date(st.last_ok_at));
  if (failing) {
    return (
      <span className="flex items-start gap-1.5 text-sm text-destructive">
        <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
        <span>
          Failing since {ago(st.last_error_at)}: {st.last_error}
        </span>
      </span>
    );
  }
  if (st?.last_ok_at) {
    return (
      <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <CheckCircle2 className="size-3.5 text-emerald-500" /> Worked {ago(st.last_ok_at)}
      </span>
    );
  }
  return <span className="text-sm text-muted-foreground">Not used yet</span>;
}

// NodeDestinations is a node's backup destinations: their health, and
// adding, testing, changing, and removing them. Saving and removing are
// signed by the user's passkey: they decide where servers' files go.
export function NodeDestinations({
  nodeId,
  userId,
  nodeName,
}: {
  nodeId: string;
  userId: string;
  nodeName: string;
}) {
  const client = useQueryClient();
  const list = useDestinations(nodeId);
  const [editing, setEditing] = useState<Destination | "new" | null>(null);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState<{ id: string; ok: boolean; text: string } | null>(null);

  async function test(d: Destination) {
    setBusy(`test-${d.id}`);
    setNotice(null);
    try {
      await nodeCommand(nodeId, "backup.destination.test", { id: d.id });
      setNotice({
        id: d.id,
        ok: true,
        text: "It works: Raptor wrote a file there, read it back, and removed it.",
      });
    } catch (err) {
      setNotice({ id: d.id, ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  async function remove(d: Destination) {
    if (
      !window.confirm(
        `Remove "${d.name}"? Raptor forgets the backups there; the files themselves are left where they are.`,
      )
    )
      return;
    setBusy(`delete-${d.id}`);
    setNotice(null);
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "backup.destination.delete",
        params: { id: d.id },
      });
      await client.invalidateQueries({ queryKey: ["backup-destinations", nodeId] });
    } catch (err) {
      if (!passkeyCancelled(err)) setNotice({ id: d.id, ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  const dests = list.data?.destinations ?? [];
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
          <CardTitle>Backup destinations</CardTitle>
          <CardDescription>
            Where {nodeName}'s servers can keep backups. Each server picks one or more of these,
            with how many backups to keep at each. The node runs them itself, even when Raptor can't
            be reached.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing("new")}>
          <Plus /> Add destination
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
        {list.isPending && <p className="text-sm text-muted-foreground">Asking the node…</p>}
        <ul className="divide-y rounded-lg border">
          {dests.map((d) => {
            const Icon = typeIcons[d.type] ?? Cloud;
            return (
              <li key={d.id} className="flex flex-col gap-2 p-3 sm:flex-row sm:items-start">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
                  <Icon className="size-4 text-muted-foreground" />
                </span>
                <div className="min-w-0 flex-1 space-y-1">
                  <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                    {d.name}
                    <Badge variant="outline">{typeNames[d.type]}</Badge>
                  </p>
                  <p className="truncate font-mono text-xs text-muted-foreground">{where(d)}</p>
                  <Health d={d} />
                  <p className="text-xs text-muted-foreground">
                    {d.status && d.status.size >= 0
                      ? `${formatBytes(d.status.size)} stored`
                      : "Size not measured yet"}
                    {d.type !== "local" && ` · Upload: ${formatSpeed(d.upload_limit)}`}
                  </p>
                  {notice?.id === d.id && (
                    <p
                      className={cn(
                        "text-sm",
                        notice.ok ? "text-emerald-600 dark:text-emerald-400" : "text-destructive",
                      )}
                    >
                      {notice.text}
                    </p>
                  )}
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button size="sm" variant="ghost" disabled={busy !== ""} onClick={() => test(d)}>
                    {busy === `test-${d.id}` ? <Loader2 className="animate-spin" /> : null}
                    Test
                  </Button>
                  {d.type !== "local" && (
                    <>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Edit ${d.name}`}
                        disabled={busy !== ""}
                        onClick={() => setEditing(d)}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Remove ${d.name}`}
                        className="text-destructive"
                        disabled={busy !== ""}
                        onClick={() => remove(d)}
                      >
                        <Trash2 />
                      </Button>
                    </>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      </CardContent>
      {editing && (
        <DestinationDialog
          key={editing === "new" ? "new" : editing.id}
          nodeId={nodeId}
          userId={userId}
          initial={editing === "new" ? undefined : editing}
          sshPublicKey={list.data?.ssh_public_key ?? ""}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null);
            await client.invalidateQueries({ queryKey: ["backup-destinations", nodeId] });
          }}
        />
      )}
    </Card>
  );
}

const choosable: Exclude<DestinationType, "local">[] = ["s3", "sftp", "folder", "webdav", "azure"];

const addTitles: Record<Exclude<DestinationType, "local">, string> = {
  s3: "Add S3-compatible storage",
  sftp: "Add an SFTP server",
  folder: "Add a folder on the node",
  webdav: "Add a WebDAV folder",
  azure: "Add Azure Blob Storage",
};

// defaultName is what a new destination is called until it's renamed.
const defaultName = (t: Exclude<DestinationType, "local">, preset?: string) =>
  t === "s3" ? (s3Presets.find((p) => p.id === preset)?.name ?? "S3") : typeNames[t];

// Form state: every type's fields, as strings, so switching type keeps what
// was typed.
type Form = {
  name: string;
  uploadMBs: string;
  folder: string;
  preset: string;
  region: string;
  endpoint: string;
  bucket: string;
  prefix: string;
  accessKey: string;
  secretKey: string;
  azAccount: string;
  azContainer: string;
  azPrefix: string;
  azAuth: "key" | "sas";
  azSecret: string;
  sftpHost: string;
  sftpPort: string;
  sftpUser: string;
  sftpPath: string;
  sftpAuth: "node" | "password" | "key";
  sftpSecret: string;
  hostKey: string;
  davURL: string;
  davUser: string;
  davPassword: string;
};

function formFrom(d?: Destination): Form {
  const s3 = d?.s3;
  const preset = s3 ? presetFor(s3.endpoint) : s3Presets[0];
  return {
    name: d?.name ?? "",
    uploadMBs: d?.upload_limit ? String(d.upload_limit / 1_000_000) : "",
    folder: d?.folder?.path ?? "/mnt/backups/raptor",
    preset: preset?.id ?? "b2",
    region: s3 && preset ? regionOf(preset, s3.endpoint) : "",
    endpoint: s3?.endpoint ?? "",
    bucket: s3?.bucket ?? "",
    prefix: s3?.prefix ?? "raptor/",
    accessKey: s3?.access_key ?? "",
    secretKey: "", // secrets never come back; blank keeps the saved one
    azAccount: d?.azure?.storage_account ?? "",
    azContainer: d?.azure?.container ?? "",
    azPrefix: d?.azure?.prefix ?? "raptor/",
    azAuth: d?.azure?.sas_token ? "sas" : "key",
    azSecret: "",
    sftpHost: d?.sftp?.host ?? "",
    sftpPort: d?.sftp?.port ? String(d.sftp.port) : "22",
    sftpUser: d?.sftp?.username ?? "",
    sftpPath: d?.sftp?.path ?? "raptor-backups",
    sftpAuth: d?.sftp
      ? d.sftp.use_node_key
        ? "node"
        : d.sftp.password
          ? "password"
          : "key"
      : "node",
    sftpSecret: "",
    hostKey: d?.sftp?.host_key ?? "",
    davURL: d?.webdav?.url ?? "",
    davUser: d?.webdav?.username ?? "",
    davPassword: "",
  };
}

// kept is what a secret field left blank sends: "********" keeps the saved
// one, when there is one for the same way of signing in.
const kept = (typed: string, had: boolean) => (typed || !had ? typed : "********");

function toDestination(
  type: Exclude<DestinationType, "local">,
  f: Form,
  orig?: Destination,
): Destination {
  const o = formFrom(orig);
  const d: Destination = {
    id: orig?.id ?? "",
    name: f.name.trim(),
    type,
    upload_limit: f.uploadMBs ? Math.round(Number(f.uploadMBs) * 1_000_000) : 0,
  };
  switch (type) {
    case "folder":
      d.folder = { path: f.folder.trim() };
      break;
    case "s3": {
      const p = s3Presets.find((x) => x.id === f.preset);
      const endpoint = p && p.id !== "custom" ? presetEndpoint(p, f.region) : f.endpoint.trim();
      d.s3 = {
        endpoint,
        region: f.preset === "aws" || f.preset === "wasabi" ? f.region.trim() : undefined,
        bucket: f.bucket.trim(),
        prefix: f.prefix.trim(),
        access_key: f.accessKey.trim(),
        secret_key: kept(f.secretKey, !!orig?.s3?.secret_key),
      };
      break;
    }
    case "azure":
      d.azure = {
        storage_account: f.azAccount.trim(),
        container: f.azContainer.trim(),
        prefix: f.azPrefix.trim(),
        storage_key: f.azAuth === "key" ? kept(f.azSecret, !!orig?.azure?.storage_key) : "",
        sas_token: f.azAuth === "sas" ? kept(f.azSecret, !!orig?.azure?.sas_token) : "",
      };
      break;
    case "sftp":
      d.sftp = {
        host: f.sftpHost.trim(),
        port: Number(f.sftpPort) || 22,
        username: f.sftpUser.trim(),
        path: f.sftpPath.trim(),
        host_key: f.hostKey,
        use_node_key: f.sftpAuth === "node",
        password:
          f.sftpAuth === "password"
            ? kept(f.sftpSecret, !!orig?.sftp?.password && o.sftpAuth === "password")
            : "",
        private_key:
          f.sftpAuth === "key"
            ? kept(f.sftpSecret, !!orig?.sftp?.private_key && o.sftpAuth === "key")
            : "",
      };
      break;
    case "webdav":
      d.webdav = {
        url: f.davURL.trim(),
        username: f.davUser.trim(),
        password: kept(f.davPassword, !!orig?.webdav?.password),
      };
      break;
  }
  return d;
}

function DestinationDialog({
  nodeId,
  userId,
  initial,
  sshPublicKey,
  onClose,
  onSaved,
}: {
  nodeId: string;
  userId: string;
  initial?: Destination;
  sshPublicKey: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [type, setType] = useState<Exclude<DestinationType, "local"> | null>(
    initial && initial.type !== "local" ? initial.type : null,
  );
  const [f, setF] = useState<Form>(() => formFrom(initial));
  const [busy, setBusy] = useState<"" | "test" | "save" | "hostkey">("");
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [fetched, setFetched] = useState<{ key: string; fingerprint: string } | null>(null);
  const set = (change: Partial<Form>) => {
    setF({ ...f, ...change });
    setResult(null);
  };
  const editing = !!initial;
  const secretHint = editing ? "Saved; leave blank to keep it" : "";

  async function test() {
    if (!type) return;
    setBusy("test");
    setResult(null);
    try {
      await nodeCommand(nodeId, "backup.destination.test", toDestination(type, f, initial));
      setResult({
        ok: true,
        text: "It works: the node wrote a file there, read it back, and removed it.",
      });
    } catch (err) {
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  async function fetchHostKey() {
    setBusy("hostkey");
    setResult(null);
    try {
      const k = await nodeCommand<{ key: string; fingerprint: string }>(nodeId, "backup.hostkey", {
        host: f.sftpHost.trim(),
        port: Number(f.sftpPort) || 22,
      });
      setFetched(k);
    } catch (err) {
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!type) return;
    setBusy("save");
    setResult(null);
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "backup.destination.save",
        params: toDestination(type, f, initial) as unknown as Record<string, unknown>,
      });
      await onSaved();
    } catch (err) {
      if (!passkeyCancelled(err)) setResult({ ok: false, text: message(err) });
      setBusy("");
    }
  }

  const preset = s3Presets.find((p) => p.id === f.preset) ?? s3Presets[0];
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        {!type ? (
          <div className="flex min-w-0 flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Add a backup destination</DialogTitle>
              <DialogDescription>Where should this node keep backups?</DialogDescription>
            </DialogHeader>
            <div className="grid gap-2">
              {choosable.map((t) => {
                const Icon = typeIcons[t];
                return (
                  <button
                    key={t}
                    type="button"
                    onClick={() => {
                      setType(t);
                      // A name still at the last type's default follows the new type.
                      const auto =
                        !f.name || choosable.some((c) => f.name === defaultName(c, f.preset));
                      if (auto) set({ name: defaultName(t, f.preset) });
                    }}
                    className="flex items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50"
                  >
                    <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                    <span>
                      <span className="block text-sm font-medium">{typeNames[t]}</span>
                      <span className="block text-xs text-muted-foreground">{typeHints[t]}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        ) : (
          <form onSubmit={save} className="flex min-w-0 flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{editing ? `Edit "${initial?.name}"` : addTitles[type]}</DialogTitle>
              <DialogDescription>{typeHints[type]}</DialogDescription>
            </DialogHeader>
            <div className="-mx-1 flex max-h-[60vh] min-w-0 flex-col gap-4 overflow-y-auto px-1">
              <Field label="Name" id="d-name">
                <Input
                  id="d-name"
                  required
                  maxLength={100}
                  value={f.name}
                  onChange={(e) => set({ name: e.target.value })}
                />
              </Field>

              {type === "folder" && (
                <Field
                  label="Folder"
                  id="d-folder"
                  hint="In /mnt, /media, or /srv on the node, where disks and NAS shares are mounted."
                >
                  <Input
                    id="d-folder"
                    required
                    className="font-mono"
                    value={f.folder}
                    onChange={(e) => set({ folder: e.target.value })}
                  />
                </Field>
              )}

              {type === "s3" && (
                <>
                  <Field label="Provider" id="d-preset">
                    <select
                      id="d-preset"
                      className={select}
                      value={f.preset}
                      onChange={(e) => {
                        const p = s3Presets.find((x) => x.id === e.target.value);
                        set({
                          preset: e.target.value,
                          region: "",
                          name: f.name === preset?.name || !f.name ? (p?.name ?? f.name) : f.name,
                        });
                      }}
                    >
                      {s3Presets.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {preset?.id === "custom" ? (
                    <Field
                      label="Endpoint"
                      id="d-endpoint"
                      hint="A host name, or http://host:port for storage on your own network."
                    >
                      <Input
                        id="d-endpoint"
                        required
                        className="font-mono"
                        placeholder="minio.example.com"
                        value={f.endpoint}
                        onChange={(e) => set({ endpoint: e.target.value })}
                      />
                    </Field>
                  ) : (
                    preset?.endpoint.includes("{region}") && (
                      <Field
                        label={preset.id === "r2" ? "Account ID" : "Region"}
                        id="d-region"
                        hint={`Endpoint: ${presetEndpoint(preset, f.region)}`}
                      >
                        <Input
                          id="d-region"
                          required
                          className="font-mono"
                          placeholder={preset.regionHint}
                          value={f.region}
                          onChange={(e) => set({ region: e.target.value })}
                        />
                      </Field>
                    )
                  )}
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Bucket" id="d-bucket">
                      <Input
                        id="d-bucket"
                        required
                        className="font-mono"
                        value={f.bucket}
                        onChange={(e) => set({ bucket: e.target.value })}
                      />
                    </Field>
                    <Field label="Folder in the bucket" id="d-prefix">
                      <Input
                        id="d-prefix"
                        className="font-mono"
                        value={f.prefix}
                        onChange={(e) => set({ prefix: e.target.value })}
                      />
                    </Field>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label={preset?.keyNames[0] ?? "Access key"} id="d-ak">
                      <Input
                        id="d-ak"
                        required
                        autoComplete="off"
                        className="font-mono"
                        value={f.accessKey}
                        onChange={(e) => set({ accessKey: e.target.value })}
                      />
                    </Field>
                    <Field label={preset?.keyNames[1] ?? "Secret key"} id="d-sk">
                      <Input
                        id="d-sk"
                        type="password"
                        required={!editing}
                        autoComplete="new-password"
                        placeholder={secretHint}
                        value={f.secretKey}
                        onChange={(e) => set({ secretKey: e.target.value })}
                      />
                    </Field>
                  </div>
                </>
              )}

              {type === "azure" && (
                <>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Storage account" id="d-az-account">
                      <Input
                        id="d-az-account"
                        required
                        className="font-mono"
                        value={f.azAccount}
                        onChange={(e) => set({ azAccount: e.target.value })}
                      />
                    </Field>
                    <Field label="Container" id="d-az-container">
                      <Input
                        id="d-az-container"
                        required
                        className="font-mono"
                        value={f.azContainer}
                        onChange={(e) => set({ azContainer: e.target.value })}
                      />
                    </Field>
                  </div>
                  <Field label="Folder in the container" id="d-az-prefix">
                    <Input
                      id="d-az-prefix"
                      className="font-mono"
                      value={f.azPrefix}
                      onChange={(e) => set({ azPrefix: e.target.value })}
                    />
                  </Field>
                  <Choice
                    label="Sign in with"
                    value={f.azAuth}
                    options={[
                      ["key", "Account key"],
                      ["sas", "SAS token"],
                    ]}
                    onChange={(v) => set({ azAuth: v as Form["azAuth"], azSecret: "" })}
                  />
                  <Field label={f.azAuth === "key" ? "Account key" : "SAS token"} id="d-az-secret">
                    <Input
                      id="d-az-secret"
                      type="password"
                      required={!editing}
                      autoComplete="new-password"
                      placeholder={secretHint}
                      value={f.azSecret}
                      onChange={(e) => set({ azSecret: e.target.value })}
                    />
                  </Field>
                </>
              )}

              {type === "sftp" && (
                <>
                  <div className="grid gap-4 sm:grid-cols-[1fr_6rem]">
                    <Field label="Host" id="d-host">
                      <Input
                        id="d-host"
                        required
                        className="font-mono"
                        placeholder="backup.example.com"
                        value={f.sftpHost}
                        onChange={(e) => {
                          set({ sftpHost: e.target.value, hostKey: "" });
                          setFetched(null);
                        }}
                      />
                    </Field>
                    <Field label="Port" id="d-port">
                      <Input
                        id="d-port"
                        inputMode="numeric"
                        className="font-mono"
                        value={f.sftpPort}
                        onChange={(e) => {
                          set({ sftpPort: e.target.value, hostKey: "" });
                          setFetched(null);
                        }}
                      />
                    </Field>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Username" id="d-user">
                      <Input
                        id="d-user"
                        required
                        className="font-mono"
                        value={f.sftpUser}
                        onChange={(e) => set({ sftpUser: e.target.value })}
                      />
                    </Field>
                    <Field label="Folder" id="d-path" hint="On the server, from the user's home.">
                      <Input
                        id="d-path"
                        required
                        className="font-mono"
                        value={f.sftpPath}
                        onChange={(e) => set({ sftpPath: e.target.value })}
                      />
                    </Field>
                  </div>
                  <HostKey
                    pinned={f.hostKey}
                    fetched={fetched}
                    busy={busy === "hostkey"}
                    canFetch={!!f.sftpHost.trim()}
                    onFetch={fetchHostKey}
                    onTrust={() => {
                      if (fetched) set({ hostKey: fetched.key });
                      setFetched(null);
                    }}
                  />
                  <Choice
                    label="Sign in with"
                    value={f.sftpAuth}
                    options={[
                      ["node", "This node's key"],
                      ["password", "Password"],
                      ["key", "A private key"],
                    ]}
                    onChange={(v) => set({ sftpAuth: v as Form["sftpAuth"], sftpSecret: "" })}
                  />
                  {f.sftpAuth === "node" ? (
                    <div className="space-y-1.5 rounded-lg border bg-muted/30 p-3 text-sm">
                      <p className="flex items-center gap-1.5 font-medium">
                        <KeyRound className="size-3.5" /> Add this line to the user's
                        ~/.ssh/authorized_keys on the server
                      </p>
                      <CopyLine value={sshPublicKey} />
                    </div>
                  ) : f.sftpAuth === "password" ? (
                    <Field label="Password" id="d-sftp-pw">
                      <Input
                        id="d-sftp-pw"
                        type="password"
                        required={!editing || !initial?.sftp?.password}
                        autoComplete="new-password"
                        placeholder={secretHint}
                        value={f.sftpSecret}
                        onChange={(e) => set({ sftpSecret: e.target.value })}
                      />
                    </Field>
                  ) : (
                    <Field
                      label="Private key"
                      id="d-sftp-key"
                      hint="PEM, without a passphrase. It's kept on the node only."
                    >
                      <textarea
                        id="d-sftp-key"
                        required={!editing || !initial?.sftp?.private_key}
                        rows={4}
                        placeholder={secretHint || "-----BEGIN OPENSSH PRIVATE KEY-----"}
                        className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                        value={f.sftpSecret}
                        onChange={(e) => set({ sftpSecret: e.target.value })}
                      />
                    </Field>
                  )}
                </>
              )}

              {type === "webdav" && (
                <>
                  <Field
                    label="URL"
                    id="d-dav"
                    hint="The folder's WebDAV address. Nextcloud's is https://cloud.example.com/remote.php/dav/files/USER/raptor."
                  >
                    <Input
                      id="d-dav"
                      type="url"
                      required
                      className="font-mono"
                      value={f.davURL}
                      onChange={(e) => set({ davURL: e.target.value })}
                    />
                  </Field>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Username" id="d-dav-user">
                      <Input
                        id="d-dav-user"
                        value={f.davUser}
                        onChange={(e) => set({ davUser: e.target.value })}
                      />
                    </Field>
                    <Field label="Password" id="d-dav-pw" hint="An app password, if it has them.">
                      <Input
                        id="d-dav-pw"
                        type="password"
                        autoComplete="new-password"
                        placeholder={secretHint}
                        value={f.davPassword}
                        onChange={(e) => set({ davPassword: e.target.value })}
                      />
                    </Field>
                  </div>
                </>
              )}

              {type !== "folder" && (
                <Field
                  label="Upload speed limit (MB/s)"
                  id="d-limit"
                  hint="Leave empty for none. A limit keeps backups from crowding out players on a slow uplink."
                >
                  <Input
                    id="d-limit"
                    inputMode="decimal"
                    className="w-32"
                    value={f.uploadMBs}
                    onChange={(e) => set({ uploadMBs: e.target.value })}
                  />
                </Field>
              )}
            </div>

            {result && (
              <p
                className={cn(
                  "flex items-start gap-1.5 text-sm",
                  result.ok ? "text-emerald-600 dark:text-emerald-400" : "text-destructive",
                )}
              >
                {result.ok ? (
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
                ) : (
                  <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                )}
                {result.text}
              </p>
            )}
            <DialogFooter className="sm:justify-between">
              <Button
                type="button"
                variant="outline"
                disabled={busy !== "" || (type === "sftp" && !f.hostKey)}
                onClick={test}
              >
                {busy === "test" && <Loader2 className="animate-spin" />}
                Test
              </Button>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => (editing ? onClose() : setType(null))}
                >
                  {editing ? "Cancel" : "Back"}
                </Button>
                <Button type="submit" disabled={busy !== "" || (type === "sftp" && !f.hostKey)}>
                  {busy === "save" ? "Saving…" : "Save with a passkey"}
                </Button>
              </div>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// HostKey asks the SFTP server for its host key and has the user trust it,
// so a different server answering later is refused.
function HostKey({
  pinned,
  fetched,
  busy,
  canFetch,
  onFetch,
  onTrust,
}: {
  pinned: string;
  fetched: { key: string; fingerprint: string } | null;
  busy: boolean;
  canFetch: boolean;
  onFetch: () => void;
  onTrust: () => void;
}) {
  if (fetched) {
    return (
      <div className="space-y-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-sm">
        <p className="font-medium">Is this the server's key?</p>
        <p className="text-muted-foreground">
          Compare it with what the server shows (<code>ssh-keygen -lf</code> on its host key, or
          your host's panel). From then on, a server answering with any other key is refused.
        </p>
        <p className="font-mono text-xs break-all">{fetched.fingerprint}</p>
        <Button type="button" size="sm" onClick={onTrust}>
          <ShieldCheck /> Trust this key
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3 text-sm">
      <span className="flex items-center gap-1.5">
        {pinned ? (
          <>
            <ShieldCheck className="size-4 text-emerald-500" /> Host key trusted
          </>
        ) : (
          <span className="text-muted-foreground">The server's host key isn't checked yet.</span>
        )}
      </span>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy || !canFetch}
        onClick={onFetch}
      >
        {busy && <Loader2 className="animate-spin" />}
        {pinned ? "Check again" : "Get the host key"}
      </Button>
    </div>
  );
}

function Field({
  label,
  id,
  hint,
  children,
}: {
  label: string;
  id: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function Choice({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: [string, string][];
  onChange: (v: string) => void;
}) {
  return (
    <fieldset className="grid gap-1.5">
      <legend className="mb-1.5 text-sm font-medium">{label}</legend>
      <div className="flex flex-wrap rounded-lg border p-0.5">
        {options.map(([v, text]) => (
          <button
            key={v}
            type="button"
            aria-pressed={value === v}
            onClick={() => onChange(v)}
            className={cn(
              "flex-1 rounded-md px-2.5 py-1 text-sm text-muted-foreground transition-colors hover:text-foreground",
              value === v && "bg-muted font-medium text-foreground",
            )}
          >
            {text}
          </button>
        ))}
      </div>
    </fieldset>
  );
}

function CopyLine({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-start gap-1">
      <code className="min-w-0 flex-1 rounded bg-background p-2 font-mono text-xs break-all">
        {value || "…"}
      </code>
      <Button
        type="button"
        size="icon-sm"
        variant="ghost"
        aria-label="Copy"
        disabled={!value}
        onClick={async () => {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}
