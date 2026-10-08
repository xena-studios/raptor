import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { KeyRound, Plus, Trash2, Users } from "lucide-react";
import { type FormEvent, useState } from "react";

import { AppShell } from "@/components/app-shell";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge, StatusDetail } from "@/components/server-status";
import { TrustedKeys } from "@/components/trusted-keys";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthService, type Passkey } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService, Role, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";
import { serverStatus } from "@/lib/servers";
import { requireSession } from "@/lib/session";
import { sameBytes, sendSigned, whichPasskey } from "@/lib/signed";
import { orgClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId")({
  beforeLoad: ({ location }) => requireSession(location),
  component: NodePage,
});

const allPermissions = [
  "console.read",
  "console.write",
  "power",
  "files.read",
  "files.write",
  "backups",
  "schedules",
  "startup",
  "reinstall",
  "sftp",
];

function NodePage() {
  const session = Route.useRouteContext();
  const { orgId, nodeId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const nodes = useQuery(OrgService.method.listNodes, { orgId });
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  const servers = useQuery(
    OrgService.method.listServers,
    { orgId, nodeId },
    { refetchInterval: 5_000 },
  );

  return (
    <AppShell session={session} orgId={orgId}>
      <p className="mb-1 text-sm text-muted-foreground">
        <Link to="/orgs/$orgId" params={{ orgId }} className="hover:underline">
          {org?.name ?? "Org"}
        </Link>
      </p>
      <div className="mb-6 flex items-center gap-3">
        <h1 className="font-heading text-2xl font-semibold">{node?.name ?? "Node"}</h1>
        {node && (
          <Badge variant={node.connected ? "default" : "outline"}>
            {node.connected ? "Connected" : "Offline"}
          </Badge>
        )}
        {isAdmin(org?.role) && (
          <Link
            to="/orgs/$orgId/servers/new"
            params={{ orgId }}
            search={{ node: nodeId }}
            className={buttonVariants({ size: "sm", className: "ml-auto" })}
          >
            <Plus /> New server
          </Link>
        )}
      </div>
      <div className="flex flex-col gap-3">
        {servers.data?.servers.length === 0 && (
          <p className="text-sm text-muted-foreground">No servers you can see on this node.</p>
        )}
        {servers.data?.servers.map((s) => (
          <ServerCard
            key={s.id}
            server={s}
            orgId={orgId}
            nodeId={nodeId}
            admin={isAdmin(org?.role)}
            userId={session.user?.id ?? ""}
            nodeConnected={node?.connected ?? false}
          />
        ))}
        {isAdmin(org?.role) && (
          <TrustedKeys
            orgId={orgId}
            nodeId={nodeId}
            userId={session.user?.id ?? ""}
            servers={servers.data?.servers ?? []}
          />
        )}
        {isAdmin(org?.role) && <PairKey nodeId={nodeId} userId={session.user?.id ?? ""} />}
      </div>
    </AppShell>
  );
}

function ServerCard({
  server,
  orgId,
  nodeId,
  admin,
  userId,
  nodeConnected,
}: {
  server: Server;
  orgId: string;
  nodeId: string;
  admin: boolean;
  userId: string;
  nodeConnected: boolean;
}) {
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [sharing, setSharing] = useState(false);
  const can = (p: string) => server.permissions.includes("*") || server.permissions.includes(p);
  const status = serverStatus(server, nodeConnected);

  // Deleting is signed by the user's passkey; Wings checks the signature.
  async function remove() {
    const typed = window.prompt(
      `Delete "${server.name}" and all its files? Type its name to confirm.`,
    );
    if (typed !== server.name) return;
    setBusy("server.delete");
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "server.delete",
        serverId: server.id,
      });
      await client.invalidateQueries();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-3">
        <div>
          <CardTitle className="flex items-center gap-2">
            <Link
              to="/orgs/$orgId/nodes/$nodeId/servers/$serverId"
              params={{ orgId, nodeId, serverId: server.id }}
              className="hover:underline"
            >
              {server.name}
            </Link>{" "}
            <StatusBadge status={status} />
          </CardTitle>
          <CardDescription>
            {server.eggName || "—"}
            {server.ports[0] ? ` · port ${server.ports[0]}` : ""} · SFTP ID{" "}
            <code>{server.id.slice(-8)}</code>
          </CardDescription>
          <StatusDetail status={status} />
        </div>
        <div className="flex items-center gap-1">
          {can("power") && <PowerButtons nodeId={nodeId} serverId={server.id} onError={setError} />}
          {admin && (
            <Button
              size="sm"
              variant="ghost"
              aria-label={`Delete ${server.name}`}
              disabled={!!busy}
              onClick={remove}
            >
              <Trash2 />
            </Button>
          )}
          {admin && (
            <Button
              size="sm"
              variant="ghost"
              aria-label="Who has access"
              onClick={() => setSharing(!sharing)}
            >
              <Users />
            </Button>
          )}
        </div>
      </CardHeader>
      {(error || sharing) && (
        <CardContent className="flex flex-col gap-3">
          {error && <p className="text-sm text-destructive">{error}</p>}
          {sharing && <Access orgId={orgId} nodeId={nodeId} serverId={server.id} />}
        </CardContent>
      )}
    </Card>
  );
}

// Access: what each member may do on this server (admins and owners can
// always do everything).
function Access({ orgId, nodeId, serverId }: { orgId: string; nodeId: string; serverId: string }) {
  const members = useQuery(OrgService.method.listMembers, { orgId });
  const access = useQuery(OrgService.method.listServerAccess, { orgId, nodeId, serverId });
  const client = useQueryClient();
  const [error, setError] = useState("");
  const granted = new Map(access.data?.access.map((a) => [a.userId, a.permissions]) ?? []);
  const plain = members.data?.members.filter((m) => m.role === Role.MEMBER) ?? [];

  async function toggle(userId: string, perm: string) {
    const current = granted.get(userId) ?? [];
    const next = current.includes(perm) ? current.filter((p) => p !== perm) : [...current, perm];
    setError("");
    try {
      await orgClient.setServerAccess({ orgId, nodeId, serverId, userId, permissions: next });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <div className="flex flex-col gap-2 text-sm">
      {error && <p className="text-destructive">{error}</p>}
      {plain.length === 0 && (
        <p className="text-muted-foreground">
          No members to share it with; admins and owners can already do everything.
        </p>
      )}
      {plain.map((m) => (
        <div key={m.userId} className="flex flex-col gap-1 rounded-lg border p-2">
          <span className="font-medium">{m.email}</span>
          <div className="flex flex-wrap gap-x-3 gap-y-1">
            {allPermissions.map((p) => (
              <label key={p} className="flex items-center gap-1">
                <input
                  type="checkbox"
                  checked={(granted.get(m.userId) ?? []).includes(p)}
                  onChange={() => toggle(m.userId, p)}
                />
                {p}
              </label>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

// PairKey trusts one of the user's passkeys on this node after root ran
// `raptor keys reset` there: the passkey signs keys.pair with the code
// from the box, and root confirms the fingerprint shown here on the box.
function PairKey({ nodeId, userId }: { nodeId: string; userId: string }) {
  const passkeys = useQuery(AuthService.method.listPasskeys, {});
  const [code, setCode] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const list = passkeys.data?.passkeys ?? [];

  async function pair(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      // First the password manager says which passkey (no list of allowed
      // ones: 1Password and others step aside when given one), then that
      // passkey signs the pairing, which names it.
      const id = await whichPasskey();
      const key: Passkey | undefined = list.find((p) => sameBytes(p.credentialId, id));
      if (!key)
        throw new Error("That passkey isn't on your Raptor account. Add it under Security first.");
      // The fingerprint comes from the key this page signs with, so a
      // Panel that swapped in its own key can't make them match.
      const mine = await keyFingerprint(key.publicKey);
      const res = await sendSigned({
        userId,
        nodeId,
        action: "keys.pair",
        params: {
          code,
          credential_id: b64(key.credentialId),
          public_key: b64(key.publicKey),
          user_id: userId,
          name: key.name,
        },
        expect: key.credentialId,
      });
      const theirs = (JSON.parse(res.resultJson || "{}") as { fingerprint?: string }).fingerprint;
      if (theirs && theirs !== mine)
        throw new Error("The node reports a different key. Don't confirm it on the node.");
      setFingerprint(mine);
      setCode("");
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="size-4" /> Trust a passkey on this node
        </CardTitle>
        <CardDescription>
          Deleting servers and other dangerous actions must be signed by a passkey the node trusts.
          On the node, run <code>sudo raptor keys reset</code>, then enter the code it shows. Your
          password manager asks twice: once to pick the passkey, once to sign.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {fingerprint ? (
          <Alert>
            <AlertDescription>
              Now confirm on the node that it shows this fingerprint, and only then: <br />
              <code className="text-base font-semibold">{fingerprint}</code>
            </AlertDescription>
          </Alert>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">Add a passkey in Security first.</p>
        ) : (
          <form onSubmit={pair} className="flex flex-col gap-2">
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Label htmlFor="pair-code">Pairing code</Label>
            <Input
              id="pair-code"
              required
              autoComplete="off"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
            <Button type="submit" className="self-start" disabled={busy}>
              Sign with a passkey
            </Button>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
