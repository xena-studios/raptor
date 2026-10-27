import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BadgePercent, ShieldOff } from "lucide-react";
import { useState } from "react";

import { RequestSupport } from "@/components/admin/request-support";
import { SuspendDialog } from "@/components/admin/suspend-dialog";
import { WaiverDialog } from "@/components/admin/waiver-dialog";
import { useConfirm } from "@/components/confirm";
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
import { StaffService, type StaffWaiver } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { dateOnly, since, when } from "@/lib/format";
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
  const { confirm } = useConfirm();
  const [suspending, setSuspending] = useState(false);
  const [waiving, setWaiving] = useState(false);
  const [asking, setAsking] = useState<{ id: string; name: string }>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
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
              {plural(o.members, "member")} · {plural(o.nodes, "node")} ·{" "}
              {plural(o.servers, "server")}
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
            {admin && (
              <Button variant="outline" className="justify-start" onClick={() => setWaiving(true)}>
                <BadgePercent /> {d.waiver ? "Change the waiver" : "Waive billing"}
              </Button>
            )}
            {admin && d.waiver && (
              <Button
                variant="outline"
                className="justify-start"
                onClick={async () => {
                  const ok = await confirm({
                    title: `End ${o.name}'s waiver?`,
                    description:
                      "Its nodes are charged from now on, and it needs a plan for them within 7 days or they go read-only. Its owners aren't emailed.",
                    confirm: "End the waiver",
                    destructive: true,
                  });
                  if (!ok) return;
                  setError("");
                  try {
                    await withReauth(() => staffClient.endBillingWaiver({ orgId }));
                    await client.invalidateQueries();
                  } catch (err) {
                    setError(message(err));
                  }
                }}
              >
                <BadgePercent /> End the waiver
              </Button>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Billing</CardTitle>
          <CardDescription>
            {d.waiver
              ? "Raptor waives part of this org's bill. Its owners see what's free and until when, not why."
              : "What the org pays, as Polar last said."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DetailList
            rows={[
              ["Plan", planWords(d.planStatus)],
              ["Seats", String(d.seats)],
              ...(d.waiver ? waiverRows(d.waiver) : [["Waiver", "None"] as [string, string]]),
            ]}
          />
        </CardContent>
      </Card>
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
              Reaching a node needs support access, which its owner approves with their passkey.
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
                    <TableHead className="w-28">
                      <span className="sr-only">Support</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.nodes.map((n) => (
                    <TableRow key={n.id}>
                      <TableCell>
                        <span className="flex flex-col">
                          <span className="font-medium">{n.name}</span>
                          <span className="text-xs text-muted-foreground">
                            {n.connected ? n.arch : `Last seen ${since(n.lastSeenAt)}`}
                          </span>
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {n.wingsVersion || "—"}
                      </TableCell>
                      <TableCell className="font-mono text-xs tabular-nums">{n.servers}</TableCell>
                      <TableCell className="text-right">
                        <StatusBadge status={nodeStatus(n)} />
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setAsking({ id: n.id, name: n.name })}
                        >
                          Ask for access
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
      {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
      {asking && (
        <RequestSupport
          nodeId={asking.id}
          nodeName={asking.name}
          onClose={() => setAsking(undefined)}
          onDone={() =>
            setNotice(`Asked. The org's owners and admins were emailed; it's in the Support tab.`)
          }
        />
      )}
      {waiving && (
        <WaiverDialog
          org={o.name}
          nodes={o.nodes}
          current={d.waiver}
          onSave={(w) =>
            withReauth(() => staffClient.setBillingWaiver({ orgId, ...w })).then(() =>
              client.invalidateQueries(),
            )
          }
          onClose={() => setWaiving(false)}
        />
      )}
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

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function planWords(status: string): string {
  switch (status) {
    case "":
      return "None";
    case "trialing":
      return "Free month";
    case "active":
      return "Active";
    case "past_due":
      return "Payment failed";
    default:
      return "Ended";
  }
}

function waiverRows(w: StaffWaiver): [string, string][] {
  return [
    ["Free nodes", w.allNodes ? "All" : String(w.freeNodes)],
    ["Backup Storage", w.storage ? "Free past what's included" : "Charged as usual"],
    ["Until", w.endsAt ? dateOnly(timestampDate(w.endsAt)) : "Ended by staff"],
    ["Why", w.reason],
    ["Given by", `${w.createdBy || "—"}, ${when(w.createdAt)}`],
  ];
}
