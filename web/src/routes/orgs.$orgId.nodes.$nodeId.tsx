import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Play, RotateCw, Square, Users } from "lucide-react";
import { useState } from "react";

import { AppShell } from "@/components/app-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { OrgService, Role, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";
import { requireSession } from "@/lib/session";
import { commandClient, orgClient } from "@/lib/transport";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId")({
  beforeLoad: ({ location }) => requireSession(location),
  component: NodePage,
});

const allPermissions = [
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
    <AppShell session={session}>
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
          />
        ))}
      </div>
    </AppShell>
  );
}

function ServerCard({
  server,
  orgId,
  nodeId,
  admin,
}: {
  server: Server;
  orgId: string;
  nodeId: string;
  admin: boolean;
}) {
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [sharing, setSharing] = useState(false);
  const can = (p: string) => server.permissions.includes("*") || server.permissions.includes(p);

  async function power(action: string) {
    setBusy(action);
    setError("");
    try {
      await commandClient.execute({ nodeId, action, serverId: server.id });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-3">
        <div>
          <CardTitle>{server.name}</CardTitle>
          <CardDescription>
            {server.eggName || "—"} · {server.state || "unknown"}
          </CardDescription>
        </div>
        <div className="flex items-center gap-1">
          {can("power") && (
            <>
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                onClick={() => power("server.start")}
              >
                <Play /> Start
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                onClick={() => power("server.restart")}
              >
                <RotateCw /> Restart
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                onClick={() => power("server.stop")}
              >
                <Square /> Stop
              </Button>
            </>
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
