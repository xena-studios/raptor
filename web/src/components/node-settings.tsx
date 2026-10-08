import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

// NodeSettings renames a node and removes it from the org (admins and
// owners).
export function NodeSettings({
  orgId,
  nodeId,
  name: current,
}: {
  orgId: string;
  nodeId: string;
  name: string;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const withReauth = useReauth();
  const [name, setName] = useState(current);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function rename(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await orgClient.renameNode({ orgId, nodeId, name });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    const typed = window.prompt(
      `Remove "${current}" from this org? Its servers keep running on the machine, but you can't manage them here until it's linked again. Type its name to confirm.`,
    );
    if (typed !== current) return;
    setBusy(true);
    setError("");
    try {
      await withReauth(() => orgClient.removeNode({ orgId, nodeId }));
      await client.invalidateQueries();
      await navigate({ to: "/orgs/$orgId", params: { orgId } });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Node settings</CardTitle>
        <CardDescription>
          Removing a node stops the Panel from managing it: its key is refused, its{" "}
          <code>raptornodes.net</code> name is removed, and its servers keep running on the machine.
          To stop them too, run <code>sudo raptor unlink</code> there first. Linking it again with a
          new join token brings it back.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <form onSubmit={rename} className="flex items-end gap-2">
          <div className="flex flex-1 flex-col gap-1.5">
            <Label htmlFor="node-name">Name</Label>
            <Input
              id="node-name"
              required
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline" disabled={busy || name.trim() === current}>
            Rename
          </Button>
        </form>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button variant="destructive" className="self-start" disabled={busy} onClick={remove}>
          Remove node
        </Button>
      </CardContent>
    </Card>
  );
}
