import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

// The permissions a member can be given, in the words the page uses.
const permissions: [string, string][] = [
  ["console.read", "See the console"],
  ["console.write", "Send console commands"],
  ["power", "Start and stop"],
  ["files.read", "See files"],
  ["files.write", "Change files"],
  ["sftp", "Use SFTP"],
  ["backups", "Backups"],
  ["schedules", "Schedules"],
  ["startup", "Startup settings"],
  ["reinstall", "Reinstall"],
];

// Access: what each member may do on this server (admins and owners can
// always do everything).
export function Access({
  orgId,
  nodeId,
  serverId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
}) {
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

  if (members.isPending) return null;
  if (plain.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Everyone in this org is an admin or owner, so there's no one to share it with yet. Invite
        someone as a member in{" "}
        <Link to="/orgs/$orgId/settings/members" params={{ orgId }} className="underline">
          org settings
        </Link>{" "}
        to give them just this server.
      </p>
    );
  }
  return (
    <div className="flex flex-col divide-y text-sm">
      {error && <p className="pb-3 text-destructive">{error}</p>}
      {plain.map((m) => (
        <div key={m.userId} className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
          <span className="font-medium">{m.name || m.email}</span>
          <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
            {permissions.map(([p, label]) => (
              <label
                key={p}
                htmlFor={`access-${m.userId}-${p}`}
                className="flex items-center gap-2"
              >
                <Checkbox
                  id={`access-${m.userId}-${p}`}
                  checked={(granted.get(m.userId) ?? []).includes(p)}
                  onCheckedChange={() => toggle(m.userId, p)}
                />
                {label}
              </label>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
