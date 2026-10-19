import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { message } from "@/lib/errors";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

// DeleteServer removes a server and its files, signed by the user's
// passkey (Wings checks the signature).
export function DeleteServer({
  orgId,
  nodeId,
  serverId,
  name,
  userId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  name: string;
  userId: string;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const ask = useConfirm();

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${name}?`,
      description:
        "Its files, local backups, schedules, and ports go with it. This can't be undone, except by restoring a backup onto a new server.",
      typeToConfirm: name,
      confirm: "Delete with passkey",
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    setError("");
    try {
      await sendSigned({ userId, nodeId, action: "server.delete", serverId });
      await client.invalidateQueries();
      await navigate({ to: "/orgs/$orgId/servers", params: { orgId } });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>Delete server</CardTitle>
        <CardDescription>
          Deletes the server and every file it has. Your passkey signs it.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <Button variant="destructive" className="self-start" disabled={busy} onClick={remove}>
          Delete server
        </Button>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
