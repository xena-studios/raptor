import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { LogOut, ShieldOff } from "lucide-react";
import { useState } from "react";

import { describeDevice } from "@/components/account/security";
import { SuspendDialog } from "@/components/admin/suspend-dialog";
import { DetailList } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { staffClient } from "@/lib/transport";

export const Route = createFileRoute("/admin/users/$userId")({
  component: UserPage,
});

function UserPage() {
  const { userId } = Route.useParams();
  const me = useQuery(StaffService.method.getStaffMe, {});
  const admin = me.data?.role === "admin";
  const user = useQuery(StaffService.method.getUser, { userId });
  const withReauth = useReauth();
  const client = useQueryClient();
  const [suspending, setSuspending] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  if (user.error) return <p className="text-sm text-destructive">{message(user.error)}</p>;
  const d = user.data;
  if (!d?.user) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const u = d.user;

  async function act(fn: () => Promise<unknown>, done: string) {
    setError("");
    setNotice("");
    try {
      await withReauth(fn);
      setNotice(done);
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Link to="/admin/users" className="text-sm text-muted-foreground hover:underline">
        ← Accounts
      </Link>
      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              {u.name || u.email}
              {u.suspendedAt && <Badge variant="destructive">Suspended</Badge>}
              {u.staffRole && (
                <Badge variant="outline" className="capitalize">
                  {u.staffRole}
                </Badge>
              )}
            </CardTitle>
            <CardDescription>{u.email}</CardDescription>
          </CardHeader>
          <CardContent>
            <DetailList
              rows={[
                [
                  "ID",
                  <code key="i" className="text-xs">
                    {u.id}
                  </code>,
                ],
                ["Joined", when(u.createdAt)],
                ["Last seen", u.lastSeenAt ? when(u.lastSeenAt) : "Never"],
                ["Passkeys", String(u.passkeys)],
                ...(u.suspendedAt
                  ? ([
                      ["Suspended", when(u.suspendedAt)],
                      ["Why", u.suspendedReason],
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
                ? "Each asks for your passkey, and goes in the staff log and theirs."
                : "Support can look; admins can act."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {admin && (
              <>
                <Button
                  variant="outline"
                  className="justify-start"
                  onClick={() =>
                    act(async () => {
                      const r = await staffClient.signOutUser({ userId });
                      return r;
                    }, "Signed out everywhere.")
                  }
                >
                  <LogOut /> Sign out everywhere
                </Button>
                {u.suspendedAt ? (
                  <Button
                    variant="outline"
                    className="justify-start"
                    onClick={() =>
                      act(() => staffClient.unsuspendUser({ userId }), "Suspension lifted.")
                    }
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
                )}
              </>
            )}
            {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Orgs</CardTitle>
          </CardHeader>
          <CardContent>
            {d.memberships.length === 0 ? (
              <p className="text-sm text-muted-foreground">In no org.</p>
            ) : (
              <DetailList
                rows={d.memberships.map((m) => [
                  m.orgName,
                  <span key={m.orgId} className="flex items-center justify-end gap-2">
                    <span className="capitalize text-muted-foreground">{m.role}</span>
                    {m.orgSuspended && <Badge variant="destructive">Suspended</Badge>}
                    <Link
                      to="/admin/orgs/$orgId"
                      params={{ orgId: m.orgId }}
                      className="text-xs underline-offset-2 hover:underline"
                    >
                      Open
                    </Link>
                  </span>,
                ])}
              />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Recent sign-ins</CardTitle>
          </CardHeader>
          <CardContent>
            {d.signIns.length === 0 ? (
              <p className="text-sm text-muted-foreground">None recorded.</p>
            ) : (
              <ul className="divide-y text-sm">
                {d.signIns.map((s, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: sign-ins are a fixed, ordered list
                  <li key={i} className="flex items-baseline justify-between gap-4 py-2">
                    <span className="text-muted-foreground">{when(s.at)}</span>
                    <span className="truncate text-right">
                      {[s.method, describeDevice(s.userAgent), s.ip].filter(Boolean).join(" · ")}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
      {suspending && (
        <SuspendDialog
          what={u.email}
          effect="They're signed out everywhere and can't sign in until it's lifted. Their orgs and servers keep running."
          onSuspend={(reason) =>
            withReauth(() => staffClient.suspendUser({ userId, reason })).then(() =>
              client.invalidateQueries(),
            )
          }
          onClose={() => setSuspending(false)}
        />
      )}
    </div>
  );
}
