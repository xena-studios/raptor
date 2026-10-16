import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

// ShownAddress sets a server's shown address: what players connect to
// instead of its node's hostname (a proxy's domain, or the owner's own), so
// the machine's address isn't on screen. Admins and owners.
export function ShownAddress({
  orgId,
  nodeId,
  serverId,
  current,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  current: string;
}) {
  const client = useQueryClient();
  const [addr, setAddr] = useState(current);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent, value = addr) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await orgClient.setServerDisplayAddress({ orgId, nodeId, serverId, displayAddress: value });
      setAddr(value);
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Shown address</CardTitle>
        <CardDescription>
          What players connect to, shown everywhere for this server instead of the node's hostname.
          Use it for a proxy like TCPShield, or your own domain. The node's hostname and IP address
          stay hidden until you reveal them. It's shown exactly as you type it: add the port
          (play.example.com:25565) if players need one.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <form onSubmit={save} className="flex items-end gap-2">
          <div className="flex flex-1 flex-col gap-1.5">
            <Label htmlFor="server-address">Address</Label>
            <Input
              id="server-address"
              placeholder="play.example.com"
              autoComplete="off"
              spellCheck={false}
              maxLength={260}
              value={addr}
              onChange={(e) => setAddr(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline" disabled={busy || addr.trim() === current}>
            Save
          </Button>
          {current && (
            <Button type="button" variant="ghost" disabled={busy} onClick={(e) => save(e, "")}>
              Use the hostname
            </Button>
          )}
        </form>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
