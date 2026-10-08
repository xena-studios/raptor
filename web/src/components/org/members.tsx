import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin, roleNames, when } from "@/lib/format";
import { orgClient } from "@/lib/transport";

const roleOptions = [Role.MEMBER, Role.ADMIN, Role.OWNER];

export function Members({
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
    <div className="flex flex-col gap-3">
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
