import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, Copy, Eye, History, KeyRound, Loader2, Plus, Table2, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { RestoreDatabaseDialog } from "@/components/db-restore";
import { DetailList } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  connectionURL,
  type Database,
  type DBInstance,
  engineName,
  labelPattern,
  serverDatabasesKey,
  useServerDatabases,
} from "@/lib/databases";
import { message } from "@/lib/errors";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// A server's Databases tab (docs/DATABASES.md#using-a-database-from-a-server):
// its databases on the node's instances, their connection details, and
// the variables the server gets for them at its next start.
export function ServerDatabases({
  orgId,
  nodeId,
  serverId,
  userId,
  canWrite,
  admin,
  supported,
  connected,
  browsable,
  transfers,
  restorable,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  canWrite: boolean;
  admin: boolean;
  supported: boolean;
  connected: boolean;
  // The node's Wings has the browser.
  browsable: boolean;
  // The node's Wings has read-only users (and imports and exports).
  transfers: boolean;
  // The node's Wings puts databases in backups, and restores one alone.
  restorable: boolean;
}) {
  const list = useServerDatabases(nodeId, serverId, supported && connected);
  const [creating, setCreating] = useState(false);
  const [shown, setShown] = useState<{ db: Database; password: string; readonly?: boolean }>();
  const [restoring, setRestoring] = useState<Database>();

  if (!supported) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          Update Wings on this node to give servers databases.
        </CardContent>
      </Card>
    );
  }
  if (!connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          The node is offline, so the server's databases can't be read until it's back.
        </CardContent>
      </Card>
    );
  }
  if (list.error) return <p className="text-sm text-destructive">{message(list.error)}</p>;
  const d = list.data;
  if (!d) return <p className="text-sm text-muted-foreground">Asking the node…</p>;
  const running = d.instances.filter((i) => i.state === "running");

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Databases</CardTitle>
          <CardDescription>
            Each database has its own user, which reaches it and nothing else. The server gets their
            details as variables at its next start: <code>DB_HOST</code>, <code>DB_PORT</code>,{" "}
            <code>DB_NAME</code>, <code>DB_USER</code>, <code>DB_PASSWORD</code>, and{" "}
            <code>DATABASE_URL</code>.
          </CardDescription>
          {canWrite && (
            <CardAction>
              <Button size="sm" disabled={running.length === 0} onClick={() => setCreating(true)}>
                <Plus /> New database
              </Button>
            </CardAction>
          )}
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {d.instances.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              There's no database instance on this server's network yet.{" "}
              {admin ? (
                <>
                  Make one on the{" "}
                  <Link
                    to="/orgs/$orgId/nodes/$nodeId/databases"
                    params={{ orgId, nodeId }}
                    className="underline underline-offset-2"
                  >
                    node's Databases tab
                  </Link>
                  .
                </>
              ) : (
                "Ask an admin of the org to add one."
              )}
            </p>
          ) : running.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              The instances this server can use aren't running.
            </p>
          ) : null}
          {d.databases.length === 0
            ? d.instances.length > 0 && (
                <p className="text-sm text-muted-foreground">No databases yet.</p>
              )
            : d.databases.map((db, i) => (
                <DatabaseRow
                  key={db.id}
                  orgId={orgId}
                  browsable={browsable}
                  transfers={transfers}
                  nodeId={nodeId}
                  serverId={serverId}
                  userId={userId}
                  db={db}
                  first={i === 0}
                  many={d.databases.length > 1}
                  canWrite={canWrite}
                  instance={d.instances.find((x) => x.id === db.instance_id)}
                  onShow={(password, readonly) => setShown({ db, password, readonly })}
                  onRestore={restorable ? () => setRestoring(db) : undefined}
                />
              ))}
        </CardContent>
      </Card>
      {creating && (
        <CreateDialog
          nodeId={nodeId}
          serverId={serverId}
          instances={running}
          onCreated={(db, password) => {
            setCreating(false);
            setShown({ db, password });
          }}
          onClose={() => setCreating(false)}
        />
      )}
      {shown && <PasswordDialog {...shown} onClose={() => setShown(undefined)} />}
      {restoring && (
        <RestoreDatabaseDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          db={restoring}
          onClose={() => setRestoring(undefined)}
        />
      )}
    </div>
  );
}

function DatabaseRow({
  orgId,
  browsable,
  transfers,
  nodeId,
  serverId,
  userId,
  db,
  first,
  many,
  canWrite,
  instance,
  onShow,
  onRestore,
}: {
  orgId: string;
  browsable: boolean;
  transfers: boolean;
  nodeId: string;
  serverId: string;
  userId: string;
  db: Database;
  first: boolean;
  many: boolean;
  canWrite: boolean;
  instance?: DBInstance;
  onShow: (password: string, readonly?: boolean) => void;
  onRestore?: () => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function act(what: string, fn: () => Promise<void>) {
    setBusy(what);
    setError("");
    try {
      await fn();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  const show = () =>
    act("show", async () => {
      const res = await sendSigned({
        userId,
        nodeId,
        serverId,
        action: "db.password",
        params: { id: db.id },
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });

  async function reset() {
    const ok = await ask.confirm({
      title: `Give ${db.label} a new password?`,
      description:
        "The old one stops working at once. The server gets the new one at its next start; anything else using it needs the new one too.",
      confirm: "New password",
    });
    if (!ok) return;
    await act("reset", async () => {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "db.password.reset",
        paramsJson: JSON.stringify({ id: db.id }),
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });
  }

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${db.label}?`,
      description: "The database and everything in it are deleted, and its user with it.",
      typeToConfirm: db.label,
      confirm: "Delete with passkey",
      destructive: true,
    });
    if (!ok) return;
    await act("delete", async () => {
      await sendSigned({ userId, nodeId, serverId, action: "db.delete", params: { id: db.id } });
      await client.invalidateQueries({ queryKey: serverDatabasesKey(nodeId, serverId) });
    });
  }

  const prefix = many ? `${db.label.toUpperCase()}_` : "";
  return (
    <div className="flex flex-col gap-3 rounded-lg border p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2 py-0.5">
          <span className="font-medium">{db.label}</span>
          <Badge variant="secondary">
            {engineName(db.engine)}
            {instance ? ` ${instance.version}` : ""}
          </Badge>
          {instance && <span className="text-xs text-muted-foreground">on {instance.name}</span>}
        </div>
        <div className="-my-0.5 flex shrink-0 gap-1">
          {browsable && (
            <Link
              to="/orgs/$orgId/servers/$serverId/databases/$databaseId"
              params={{ orgId, serverId, databaseId: db.id }}
              className={buttonVariants({ size: "sm", variant: "outline" })}
            >
              <Table2 /> Open
            </Link>
          )}
          {canWrite && (
            <Button size="sm" variant="outline" disabled={!!busy} onClick={show}>
              {busy === "show" ? <Loader2 className="animate-spin" /> : <Eye />} Password
            </Button>
          )}
          {canWrite && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`New password for ${db.label}`}
              disabled={!!busy}
              onClick={reset}
            >
              {busy === "reset" ? <Loader2 className="animate-spin" /> : <KeyRound />}
            </Button>
          )}
          {canWrite && onRestore && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Restore ${db.label} from a backup`}
              title="Restore from a backup"
              disabled={!!busy}
              onClick={onRestore}
            >
              <History />
            </Button>
          )}
          {canWrite && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Delete ${db.label}`}
              disabled={!!busy}
              onClick={remove}
            >
              {busy === "delete" ? <Loader2 className="animate-spin" /> : <Trash2 />}
            </Button>
          )}
        </div>
      </div>
      <DetailList
        rows={[
          ["Host", <Copyable key="h" value={`${db.host}:${db.port}`} />],
          ["Database", <Copyable key="d" value={db.name} />],
          ["User", <Copyable key="u" value={db.user} />],
        ]}
      />
      <p className="text-xs text-muted-foreground">
        As variables: <code>{prefix}DB_HOST</code> and the rest
        {many && first ? ", and without the prefix too, since it's the first" : ""}.
      </p>
      {transfers && (
        <ReadOnlyUser
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          db={db}
          canWrite={canWrite}
          onShow={(password) => onShow(password, true)}
        />
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  );
}

function Copyable({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <span className="flex min-w-0 items-center justify-end gap-1.5" title={value}>
      <code className="min-w-0 truncate font-mono text-sm">{value}</code>
      <button
        type="button"
        aria-label={`Copy ${value}`}
        className="shrink-0 text-muted-foreground transition-colors hover:text-foreground"
        onClick={async () => {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </span>
  );
}

function CreateDialog({
  nodeId,
  serverId,
  instances,
  onCreated,
  onClose,
}: {
  nodeId: string;
  serverId: string;
  instances: DBInstance[];
  onCreated: (db: Database, password: string) => void;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [instance, setInstance] = useState(instances[0]?.id ?? "");
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const valid = labelPattern.test(label);

  async function create(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "db.create",
        paramsJson: JSON.stringify({ instance, label }),
      });
      const out = JSON.parse(res.resultJson) as { database: Database; password: string };
      await client.invalidateQueries({ queryKey: serverDatabasesKey(nodeId, serverId) });
      onCreated(out.database, out.password);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={create} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>New database</DialogTitle>
            <DialogDescription>
              A database and a user for this server alone, with a random password.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="db-label">Name</Label>
            <Input
              id="db-label"
              autoFocus
              required
              maxLength={18}
              placeholder="luckperms"
              spellCheck={false}
              value={label}
              aria-invalid={(label !== "" && !valid) || undefined}
              onChange={(e) => setLabel(e.target.value.toLowerCase())}
            />
            <p className="text-xs text-muted-foreground">
              Lowercase letters, digits, and underscores, like what it's for.
            </p>
          </div>
          {instances.length > 1 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="db-instance">Instance</Label>
              <select
                id="db-instance"
                className={select}
                value={instance}
                onChange={(e) => setInstance(e.target.value)}
              >
                {instances.map((i) => (
                  <option key={i.id} value={i.id}>
                    {i.name} ({engineName(i.engine)} {i.version})
                  </option>
                ))}
              </select>
            </div>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !valid || !instance}>
              {busy && <Loader2 className="animate-spin" />} Create database
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PasswordDialog({
  db,
  password,
  readonly,
  onClose,
}: {
  db: Database;
  password: string;
  readonly?: boolean;
  onClose: () => void;
}) {
  const user = readonly ? (db.readonly_user ?? "") : db.user;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {readonly ? `${db.label}'s read-only user` : `${db.label}'s password`}
          </DialogTitle>
          <DialogDescription>
            {readonly
              ? "It reads every table, and changes nothing. Keep the password out of screenshots and streams."
              : "The server gets it at its next start. Keep it out of screenshots and streams."}
          </DialogDescription>
        </DialogHeader>
        <DetailList
          rows={[
            ...(readonly
              ? [["User", <Copyable key="r" value={user} />] as [string, ReactNode]]
              : []),
            ["Password", <Copyable key="p" value={password} />],
            ["URL", <Copyable key="u" value={connectionURL({ ...db, user }, password)} />],
          ]}
        />
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ReadOnlyUser is a database's read-only user, for a tool that should only
// look (a stats page, a map): made, given a new password, or removed.
function ReadOnlyUser({
  nodeId,
  serverId,
  userId,
  db,
  canWrite,
  onShow,
}: {
  nodeId: string;
  serverId: string;
  userId: string;
  db: Database;
  canWrite: boolean;
  onShow: (password: string) => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function act(what: string, fn: () => Promise<void>) {
    setBusy(what);
    setError("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: serverDatabasesKey(nodeId, serverId) });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  const set = () =>
    act("set", async () => {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "db.readonly.set",
        paramsJson: JSON.stringify({ id: db.id }),
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });

  const show = () =>
    act("show", async () => {
      const res = await sendSigned({
        userId,
        nodeId,
        serverId,
        action: "db.password",
        params: { id: db.id, readonly: true },
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });

  async function remove() {
    const ok = await ask.confirm({
      title: "Remove the read-only user?",
      description: "Anything using it can't connect any more. The database isn't touched.",
      confirm: "Remove",
      destructive: true,
    });
    if (!ok) return;
    await act("remove", async () => {
      await commandClient.execute({
        nodeId,
        serverId,
        action: "db.readonly.remove",
        paramsJson: JSON.stringify({ id: db.id }),
      });
    });
  }

  if (!db.readonly_user && !canWrite) return null;
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
      <p className="flex min-w-0 items-center gap-2 text-sm">
        <span className="text-muted-foreground">Read-only user</span>
        {db.readonly_user ? (
          <code className="truncate font-mono text-sm">{db.readonly_user}</code>
        ) : (
          <span className="text-muted-foreground">none, for tools that should only look</span>
        )}
      </p>
      {canWrite && (
        <div className="flex gap-1">
          {db.readonly_user ? (
            <>
              <Button size="sm" variant="outline" disabled={!!busy} onClick={show}>
                {busy === "show" ? <Loader2 className="animate-spin" /> : <Eye />} Password
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="New password for the read-only user"
                disabled={!!busy}
                onClick={set}
              >
                {busy === "set" ? <Loader2 className="animate-spin" /> : <KeyRound />}
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Remove the read-only user"
                disabled={!!busy}
                onClick={remove}
              >
                {busy === "remove" ? <Loader2 className="animate-spin" /> : <Trash2 />}
              </Button>
            </>
          ) : (
            <Button size="sm" variant="outline" disabled={!!busy} onClick={set}>
              {busy === "set" ? <Loader2 className="animate-spin" /> : <Plus />} Make one
            </Button>
          )}
        </div>
      )}
      {error && <p className="basis-full text-sm text-destructive">{error}</p>}
    </div>
  );
}
