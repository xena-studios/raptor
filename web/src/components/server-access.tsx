import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

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
