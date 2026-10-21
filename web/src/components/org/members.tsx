import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
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
  const ask = useConfirm();
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

  const list = members.data?.members ?? [];
  const owners = list.filter((m) => m.role === Role.OWNER).length;

  // What changing someone's role means, asked before it's changed: most
  // of all when owners give up their own.
  async function changeRole(userId: string, who: string, from: Role, to: Role) {
    if (to === from) return;
    const self = userId === myId;
    if (self && from === Role.OWNER && owners === 1) {
      setError(
        "You're the only owner. Make someone else an owner first, then change your own role.",
      );
      return;
    }
    const role = roleNames[to].toLowerCase();
    const description = self
      ? from === Role.OWNER
        ? `You'll lose what only owners can do: billing, changing anyone's role, and deleting the org. You can't undo this yourself; another owner would have to make you an owner again.`
        : `You'll ${to === Role.MEMBER ? "only have access to the servers you're given" : "lose what admins can do"}, and you can't undo this yourself.`
      : to === Role.OWNER
        ? `${who} gets everything you have: billing, changing anyone's role (yours too), and deleting the org.`
        : `${from === Role.OWNER ? `${who} stops being an owner, so no more billing, roles, or deleting the org. ` : ""}${
            to === Role.ADMIN
              ? `As an admin they can manage every server and node and invite members.`
              : `As a member they only get the servers they're given, on each server's Access tab.`
          }`;
    const ok = await ask.confirm({
      title: self
        ? `Make yourself ${article(role)} ${role}?`
        : `Make ${who} ${article(role)} ${role}?`,
      description,
      confirm: `Make ${self ? "me" : "them"} ${article(role)} ${role}`,
      destructive: self || to === Role.OWNER || from === Role.OWNER,
    });
    if (!ok) return;
    await run(() => orgClient.setMemberRole({ orgId, userId, role: to }));
  }

  async function remove(userId: string, who: string) {
    const self = userId === myId;
    if (self && list.find((m) => m.userId === myId)?.role === Role.OWNER && owners === 1) {
      setError("You're the only owner. Make someone else an owner before you leave.");
      return;
    }
    const ok = await ask.confirm({
      title: self ? "Leave the org?" : `Remove ${who}?`,
      description: self
        ? "You lose access to its servers and nodes at once. Someone in it would have to invite you back."
        : `${who} loses access to the org's servers and nodes at once. You can invite them again later.`,
      confirm: self ? "Leave" : "Remove",
      destructive: true,
    });
    if (!ok) return;
    await run(() => orgClient.removeMember({ orgId, userId }));
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
        <CardHeader>
          <CardTitle>Members</CardTitle>
          <CardDescription>
            Owners and admins manage everything; members get only the servers they're given, on each
            server's Access tab.
          </CardDescription>
        </CardHeader>
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
                    onChange={(e) => changeRole(m.userId, m.email, m.role, Number(e.target.value))}
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
                    onClick={() => remove(m.userId, m.email)}
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

function article(word: string): string {
  return /^[aeiou]/.test(word) ? "an" : "a";
}
