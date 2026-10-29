import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Eye, KeyRound, Link2, Loader2, Plus, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { PasswordDialog } from "@/components/server-databases";
import { Button } from "@/components/ui/button";
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
  type Database,
  type DBInstance,
  instancesKey,
  isRedis,
  labelPattern,
  nodeDatabasesKey,
  type SharedDatabase,
} from "@/lib/databases";
import { message } from "@/lib/errors";
import type { OrgServer } from "@/lib/org-data";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// asDatabase is a shared database as the connection details show one.
function asDatabase(d: SharedDatabase, i: DBInstance): Database {
  return {
    id: d.id,
    instance_id: d.instance_id,
    server_id: "",
    label: d.label,
    name: d.db_name,
    user: d.username,
    engine: i.engine,
    host: i.host,
    port: i.port,
    created_at: d.created_at,
  };
}

// InstanceDatabases is an instance's databases (docs/DATABASES.md#the-model):
// each belongs to the instance, and the servers linked to it get its
// details as variables. Every server on the instance's networks can reach
// it; the database's own user and password are what keep it to them.
export function InstanceDatabases({
  orgId,
  nodeId,
  userId,
  instance,
  databases,
  servers,
}: {
  orgId: string;
  nodeId: string;
  userId: string;
  instance: DBInstance;
  databases: SharedDatabase[];
  // The servers on the instance's node.
  servers: OrgServer[];
}) {
  const [creating, setCreating] = useState(false);
  const [linking, setLinking] = useState<SharedDatabase>();
  const [shown, setShown] = useState<{ db: Database; password: string }>();
  // A Redis or Valkey instance holds one database.
  const full = isRedis(instance.engine) && databases.length > 0;
  const running = instance.state === "running";

  return (
    <div className="flex flex-col gap-2 border-t pt-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-muted-foreground">Databases</p>
        <Button
          size="sm"
          variant="ghost"
          disabled={!running || full}
          title={full ? "A Redis or Valkey instance holds one database" : undefined}
          onClick={() => setCreating(true)}
        >
          <Plus /> New database
        </Button>
      </div>
      {databases.length === 0 ? (
        <p className="text-xs text-muted-foreground">None yet.</p>
      ) : (
        databases.map((d) => (
          <DatabaseLine
            key={d.id}
            orgId={orgId}
            nodeId={nodeId}
            userId={userId}
            instance={instance}
            db={d}
            servers={servers}
            onLink={() => setLinking(d)}
            onShow={(password) => setShown({ db: asDatabase(d, instance), password })}
          />
        ))
      )}
      {creating && (
        <NewDatabaseDialog
          nodeId={nodeId}
          instance={instance}
          onCreated={(d, password) => {
            setCreating(false);
            setShown({ db: asDatabase(d, instance), password });
          }}
          onClose={() => setCreating(false)}
        />
      )}
      {linking && (
        <LinkDialog
          nodeId={nodeId}
          db={linking}
          servers={servers.filter((s) => !linking.links.some((l) => l.server_id === s.id))}
          onClose={() => setLinking(undefined)}
        />
      )}
      {shown && <PasswordDialog {...shown} onClose={() => setShown(undefined)} />}
    </div>
  );
}

function DatabaseLine({
  orgId,
  nodeId,
  userId,
  instance,
  db,
  servers,
  onLink,
  onShow,
}: {
  orgId: string;
  nodeId: string;
  userId: string;
  instance: DBInstance;
  db: SharedDatabase;
  servers: OrgServer[];
  onLink: () => void;
  onShow: (password: string) => void;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const name = (id: string) => servers.find((s) => s.id === id)?.name || id.slice(-8);

  async function act(what: string, fn: () => Promise<void>) {
    setBusy(what);
    setError("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: nodeDatabasesKey(nodeId) });
      await client.invalidateQueries({ queryKey: ["server-databases", nodeId] });
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
        action: "node.db.database.password",
        params: { id: db.id },
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });

  async function reset() {
    const ok = await ask.confirm({
      title: `Give ${db.label} a new password?`,
      description:
        "The old one stops working at once. Linked servers get the new one at their next start; anything else using it needs the new one too.",
      confirm: "New password",
    });
    if (!ok) return;
    await act("reset", async () => {
      const res = await commandClient.execute({
        nodeId,
        action: "node.db.database.password.reset",
        paramsJson: JSON.stringify({ id: db.id }),
      });
      onShow((JSON.parse(res.resultJson) as { password: string }).password);
    });
  }

  async function unlink(serverId: string) {
    const ok = await ask.confirm({
      title: `Unlink ${name(serverId)}?`,
      description: `It stops getting ${db.label}'s details at its next start. The database and its data stay.`,
      confirm: "Unlink",
    });
    if (!ok) return;
    await act(`unlink-${serverId}`, async () => {
      await commandClient.execute({
        nodeId,
        action: "node.db.database.unlink",
        paramsJson: JSON.stringify({ id: db.id, server_id: serverId }),
      });
    });
  }

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${db.label}?`,
      description:
        db.links.length > 0
          ? `The database and everything in it are deleted, and its user with it. ${db.links.length === 1 ? "The server linked to it loses" : `The ${db.links.length} servers linked to it lose`} its data.`
          : "The database and everything in it are deleted, and its user with it.",
      typeToConfirm: db.label,
      confirm: "Delete with passkey",
      destructive: true,
    });
    if (!ok) return;
    await act("delete", async () => {
      await sendSigned({
        userId,
        nodeId,
        action: "node.db.database.delete",
        params: { id: db.id },
      });
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
    });
  }

  return (
    <div className="flex flex-col gap-1.5 rounded-md bg-muted/40 px-2.5 py-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{db.label}</span>
          <code className="truncate font-mono text-xs text-muted-foreground">{db.db_name}</code>
        </div>
        <div className="flex shrink-0 gap-1">
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={onLink}>
            <Link2 /> Link server
          </Button>
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={show}>
            {busy === "show" ? <Loader2 className="animate-spin" /> : <Eye />} Password
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`New password for ${db.label}`}
            disabled={!!busy}
            onClick={reset}
          >
            {busy === "reset" ? <Loader2 className="animate-spin" /> : <KeyRound />}
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Delete ${db.label}`}
            disabled={!!busy}
            onClick={remove}
          >
            {busy === "delete" ? <Loader2 className="animate-spin" /> : <Trash2 />}
          </Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
        {db.links.length === 0 ? (
          <span>
            No servers linked. Servers on {instance.name}'s network can still connect with its user
            and password.
          </span>
        ) : (
          <>
            <span>Linked to</span>
            {db.links.map((l) => (
              <span
                key={l.server_id}
                className="inline-flex items-center gap-1 rounded-md border bg-background py-0.5 pr-0.5 pl-1.5"
              >
                <Link
                  to="/orgs/$orgId/servers/$serverId/databases"
                  params={{ orgId, serverId: l.server_id }}
                  className="text-foreground hover:underline"
                >
                  {name(l.server_id)}
                </Link>
                <button
                  type="button"
                  aria-label={`Unlink ${name(l.server_id)}`}
                  disabled={!!busy}
                  className="rounded-sm p-0.5 hover:bg-accent hover:text-foreground"
                  onClick={() => unlink(l.server_id)}
                >
                  {busy === `unlink-${l.server_id}` ? (
                    <Loader2 className="size-3 animate-spin" />
                  ) : (
                    <X className="size-3" />
                  )}
                </button>
              </span>
            ))}
          </>
        )}
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  );
}

function NewDatabaseDialog({
  nodeId,
  instance,
  onCreated,
  onClose,
}: {
  nodeId: string;
  instance: DBInstance;
  onCreated: (db: SharedDatabase, password: string) => void;
  onClose: () => void;
}) {
  const client = useQueryClient();
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
        action: "node.db.database.create",
        paramsJson: JSON.stringify({ instance_id: instance.id, label }),
      });
      const out = JSON.parse(res.resultJson) as {
        id: string;
        db_name: string;
        username: string;
        password: string;
      };
      await client.invalidateQueries({ queryKey: nodeDatabasesKey(nodeId) });
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
      onCreated(
        {
          id: out.id,
          instance_id: instance.id,
          label,
          db_name: out.db_name,
          username: out.username,
          created_at: Math.floor(Date.now() / 1000),
          links: [],
        },
        out.password,
      );
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
            <DialogTitle>New database on {instance.name}</DialogTitle>
            <DialogDescription>
              A database and its own user, with a random password. It stays when servers are
              deleted; link the servers that should get its details.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="shared-db-label">Name</Label>
            <Input
              id="shared-db-label"
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
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !valid}>
              {busy && <Loader2 className="animate-spin" />} Create database
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// LinkDialog links a server to a database: it gets the database's details
// as variables at its next start. The node refuses a server that isn't on
// one of the instance's networks.
function LinkDialog({
  nodeId,
  db,
  servers,
  onClose,
}: {
  nodeId: string;
  db: SharedDatabase;
  servers: OrgServer[];
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [server, setServer] = useState(servers[0]?.id ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function link(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "node.db.database.link",
        paramsJson: JSON.stringify({ id: db.id, server_id: server }),
      });
      await client.invalidateQueries({ queryKey: nodeDatabasesKey(nodeId) });
      await client.invalidateQueries({ queryKey: ["server-databases", nodeId] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={link} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Link a server to {db.label}</DialogTitle>
            <DialogDescription>
              The server gets its details as variables at its next start. It has to be on the
              instance's network.
            </DialogDescription>
          </DialogHeader>
          {servers.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Every server on this node is linked to it already.
            </p>
          ) : (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="link-server">Server</Label>
              <select
                id="link-server"
                className={select}
                value={server}
                onChange={(e) => setServer(e.target.value)}
              >
                {servers.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name || s.id.slice(-8)}
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
            <Button type="submit" disabled={busy || !server}>
              {busy && <Loader2 className="animate-spin" />} Link server
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
