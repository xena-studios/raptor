import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Server, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { AppShell } from "@/components/app-shell";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin, roleNames, when } from "@/lib/format";
import { requireSession } from "@/lib/session";
import { orgClient } from "@/lib/transport";

export const Route = createFileRoute("/orgs/$orgId/")({
  beforeLoad: ({ location }) => requireSession(location),
  component: OrgPage,
});

function useOrg(orgId: string) {
  const orgs = useQuery(OrgService.method.listOrgs, {});
  return orgs.data?.orgs.find((o) => o.id === orgId);
}

function OrgPage() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  const org = useOrg(orgId);
  const admin = isAdmin(org?.role);
  return (
    <AppShell session={session}>
      <div className="mb-6 flex items-center gap-3">
        <h1 className="font-heading text-2xl font-semibold">{org?.name ?? "…"}</h1>
        {org && <Badge variant="secondary">{roleNames[org.role]}</Badge>}
      </div>
      <Tabs defaultValue="nodes">
        <TabsList>
          <TabsTrigger value="nodes">Nodes</TabsTrigger>
          <TabsTrigger value="members">Members</TabsTrigger>
          {admin && <TabsTrigger value="log">Log</TabsTrigger>}
        </TabsList>
        <TabsContent value="nodes">
          <Nodes orgId={orgId} admin={admin} />
        </TabsContent>
        <TabsContent value="members">
          <Members orgId={orgId} myRole={org?.role} myId={session.user?.id ?? ""} />
        </TabsContent>
        {admin && (
          <TabsContent value="log">
            <Log orgId={orgId} />
          </TabsContent>
        )}
      </Tabs>
    </AppShell>
  );
}

function Nodes({ orgId, admin }: { orgId: string; admin: boolean }) {
  const nodes = useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 10_000 });
  const withReauth = useReauth();
  const [token, setToken] = useState<string | null>(null);
  const [error, setError] = useState("");

  async function addNode() {
    setError("");
    try {
      const res = await withReauth(() => orgClient.createJoinToken({ orgId }));
      setToken(res.token);
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <div className="mt-4 flex flex-col gap-3">
      {nodes.data?.nodes.length === 0 && (
        <p className="text-sm text-muted-foreground">No nodes yet.</p>
      )}
      {nodes.data?.nodes.map((n) => (
        <Link key={n.id} to="/orgs/$orgId/nodes/$nodeId" params={{ orgId, nodeId: n.id }}>
          <Card className="transition-colors hover:bg-muted/50">
            <CardHeader className="flex-row items-center justify-between">
              <div className="flex items-center gap-3">
                <Server className="size-4 text-muted-foreground" />
                <div>
                  <CardTitle>{n.name}</CardTitle>
                  <CardDescription>
                    n-{n.shortId}.raptornodes.net · Wings {n.wingsVersion || "?"}
                  </CardDescription>
                </div>
              </div>
              <Badge variant={n.connected ? "default" : "outline"}>
                {n.connected ? "Connected" : `Last seen ${when(n.lastSeenAt)}`}
              </Badge>
            </CardHeader>
          </Card>
        </Link>
      ))}
      {admin && !token && (
        <Button className="self-start" onClick={addNode}>
          Add a node
        </Button>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
      {token && (
        <Alert>
          <AlertDescription className="flex flex-col gap-2">
            <p>
              On a fresh Debian 12, Debian 13, or Ubuntu 24.04 server, run this as root. The token
              works once, for an hour.
            </p>
            <code className="break-all rounded bg-muted p-2 text-xs">
              curl -fsSL https://get.raptorpanel.net | sudo bash -s -- -token {token}
            </code>
            <p className="text-xs text-muted-foreground">
              The node shows up here as soon as it connects.
            </p>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  navigator.clipboard.writeText(
                    `curl -fsSL https://get.raptorpanel.net | sudo bash -s -- -token ${token}`,
                  )
                }
              >
                Copy
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setToken(null)}>
                Done
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}

const roleOptions = [Role.MEMBER, Role.ADMIN, Role.OWNER];

function Members({
  orgId,
  myRole,
  myId,
}: {
  orgId: string;
  myRole: Role | undefined;
  myId: string;
}) {
  const members = useQuery(OrgService.method.listMembers, { orgId });
  const admin = isAdmin(myRole);
  const owner = myRole === Role.OWNER;
  const invitations = useQuery(OrgService.method.listInvitations, { orgId }, { enabled: admin });
  const client = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>(Role.MEMBER);
  const [error, setError] = useState("");
  const [sent, setSent] = useState("");

  async function run(fn: () => Promise<unknown>) {
    setError("");
    try {
      await fn();
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  async function invite(e: FormEvent) {
    e.preventDefault();
    setSent("");
    await run(async () => {
      await orgClient.inviteMember({ orgId, email, role });
      setSent(email);
      setEmail("");
    });
  }

  return (
    <div className="mt-4 flex flex-col gap-3">
      {error && <p className="text-sm text-destructive">{error}</p>}
      <Card>
        <CardContent className="flex flex-col divide-y">
          {members.data?.members.map((m) => (
            <div key={m.userId} className="flex items-center justify-between gap-3 py-2 text-sm">
              <span>
                {m.email}{" "}
                {m.userId === myId && <span className="text-muted-foreground">(you)</span>}
              </span>
              <div className="flex items-center gap-2">
                {owner ? (
                  <select
                    className="rounded-md border bg-background px-2 py-1 text-sm"
                    value={m.role}
                    onChange={(e) =>
                      run(() =>
                        orgClient.setMemberRole({
                          orgId,
                          userId: m.userId,
                          role: Number(e.target.value),
                        }),
                      )
                    }
                  >
                    {roleOptions.map((r) => (
                      <option key={r} value={r}>
                        {roleNames[r]}
                      </option>
                    ))}
                  </select>
                ) : (
                  <Badge variant="secondary">{roleNames[m.role]}</Badge>
                )}
                {(m.userId === myId || owner || (admin && m.role === Role.MEMBER)) && (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={m.userId === myId ? "Leave the org" : `Remove ${m.email}`}
                    onClick={() => run(() => orgClient.removeMember({ orgId, userId: m.userId }))}
                  >
                    <Trash2 />
                  </Button>
                )}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      {admin && (
        <Card>
          <CardHeader>
            <CardTitle>Invite someone</CardTitle>
            <CardDescription>
              They'll get an email with a link that works for 7 days, only for that address.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <form onSubmit={invite} className="flex gap-2">
              <Input
                type="email"
                placeholder="Email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
              <select
                className="rounded-md border bg-background px-2 text-sm"
                value={role}
                onChange={(e) => setRole(Number(e.target.value))}
              >
                {roleOptions
                  .filter((r) => owner || r !== Role.OWNER)
                  .map((r) => (
                    <option key={r} value={r}>
                      {roleNames[r]}
                    </option>
                  ))}
              </select>
              <Button type="submit">Invite</Button>
            </form>
            {sent && <p className="text-sm text-muted-foreground">Invitation sent to {sent}.</p>}
            {invitations.data?.invitations.map((i) => (
              <div key={i.id} className="flex items-center justify-between text-sm">
                <span>
                  {i.email}{" "}
                  <span className="text-muted-foreground">
                    · {roleNames[i.role]} · until {when(i.expiresAt)}
                  </span>
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    run(() => orgClient.revokeInvitation({ orgId, invitationId: i.id }))
                  }
                >
                  Revoke
                </Button>
              </div>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function Log({ orgId }: { orgId: string }) {
  const log = useQuery(OrgService.method.listAuditLog, { orgId });
  return (
    <Card className="mt-4">
      <CardContent className="flex flex-col divide-y text-sm">
        {log.data?.events.map((e) => {
          const meta = JSON.parse(e.metadataJson || "{}") as Record<string, unknown>;
          const what =
            e.action === "command" ? `${meta.action}${meta.error ? " (failed)" : ""}` : e.action;
          return (
            <div key={e.id} className="flex justify-between gap-4 py-2">
              <span>
                <span className="font-medium">{e.actorEmail || "someone who left"}</span> {what}
                {e.target && <span className="text-muted-foreground"> · {e.target}</span>}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">{when(e.at)}</span>
            </div>
          );
        })}
        {log.data?.events.length === 0 && (
          <p className="py-2 text-muted-foreground">Nothing yet.</p>
        )}
      </CardContent>
    </Card>
  );
}
