import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ShieldOff } from "lucide-react";
import { useState } from "react";

import { SuspendDialog } from "@/components/admin/suspend-dialog";
import { DetailList } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { StatusBadge } from "@/components/server-status";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { formatBytes } from "@/lib/metrics";
import { nodeStatus } from "@/lib/servers";
import { staffClient } from "@/lib/transport";

export const Route = createFileRoute("/admin/orgs/$orgId")({
  component: OrgPage,
});

function OrgPage() {
  const { orgId } = Route.useParams();
  const me = useQuery(StaffService.method.getStaffMe, {});
  const admin = me.data?.role === "admin";
  const org = useQuery(StaffService.method.getStaffOrg, { orgId });
  const withReauth = useReauth();
  const client = useQueryClient();
  const [suspending, setSuspending] = useState(false);
  const [error, setError] = useState("");
  if (org.error) return <p className="text-sm text-destructive">{message(org.error)}</p>;
  const d = org.data;
  if (!d?.org) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const o = d.org;

  return (
    <div className="flex flex-col gap-6">
      <Link to="/admin/orgs" className="text-sm text-muted-foreground hover:underline">
        ← Orgs
      </Link>
      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              {o.name}
              {o.suspendedAt && <Badge variant="destructive">Suspended</Badge>}
            </CardTitle>
            <CardDescription>
              {o.members} members · {o.nodes} nodes · {o.servers} servers
            </CardDescription>
          </CardHeader>
          <CardContent>
            <DetailList
              rows={[
                [
                  "ID",
                  <code key="i" className="text-xs">
                    {o.id}
                  </code>,
                ],
                ["Created", when(o.createdAt)],
                [
                  "Backup Storage",
                  d.storageBytes ? formatBytes(Number(d.storageBytes)) : "Not used",
                ],
                ...(o.suspendedAt
                  ? ([
                      ["Suspended", when(o.suspendedAt)],
                      ["Why", o.suspendedReason],
                    ] as [string, string][])
                  : []),
              ]}
            />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Actions</CardTitle>
            <CardDescription>
              {admin
                ? "Suspending stops the org being managed in the Panel. Its nodes and game servers keep running, so players aren't affected."
                : "Support can look; admins can act."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {admin &&
              (o.suspendedAt ? (
                <Button
                  variant="outline"
                  className="justify-start"
                  onClick={async () => {
                    setError("");
                    try {
                      await withReauth(() => staffClient.unsuspendOrg({ orgId }));
                      await client.invalidateQueries();
                    } catch (err) {
                      setError(message(err));
                    }
                  }}
                >
                  <ShieldOff /> Lift the suspension
                </Button>
              ) : (
                <Button
                  variant="outline"
                  className="justify-start text-destructive"
                  onClick={() => setSuspending(true)}
                >
                  <ShieldOff /> Suspend
                </Button>
              ))}
            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Members</CardTitle>
          </CardHeader>
          <CardContent>
            <DetailList
              rows={d.members.map((m) => [
                m.name || m.email,
                <span key={m.userId} className="flex items-center justify-end gap-2">
                  <span className="capitalize text-muted-foreground">{m.role}</span>
                  <Link
                    to="/admin/users/$userId"
                    params={{ userId: m.userId }}
                    className="text-xs underline-offset-2 hover:underline"
                  >
                    Open
                  </Link>
                </span>,
              ])}
            />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Nodes</CardTitle>
            <CardDescription>
              Staff can't reach a node from here: support access needs its owner's passkey.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {d.nodes.length === 0 ? (
              <p className="text-sm text-muted-foreground">No nodes.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Wings</TableHead>
                    <TableHead>Servers</TableHead>
                    <TableHead className="text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.nodes.map((n) => (
                    <TableRow key={n.id}>
                      <TableCell>
                        <span className="flex flex-col">
                          <span className="font-medium">{n.name}</span>
                          <span className="text-xs text-muted-foreground">
                            {n.connected ? n.arch : `Last seen ${when(n.lastSeenAt)}`}
                          </span>
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {n.wingsVersion || "—"}
                      </TableCell>
                      <TableCell className="tabular-nums">{n.servers}</TableCell>
                      <TableCell className="text-right">
                        <StatusBadge status={nodeStatus(n)} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
      {suspending && (
        <SuspendDialog
          what={o.name}
          effect="Its members can't manage it in the Panel until it's lifted. Its nodes and game servers keep running."
          onSuspend={(reason) =>
            withReauth(() => staffClient.suspendOrg({ orgId, reason })).then(() =>
              client.invalidateQueries(),
            )
          }
          onClose={() => setSuspending(false)}
        />
      )}
    </div>
  );
}
