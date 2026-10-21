import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { DetailList } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { Node } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { orgClient } from "@/lib/transport";

// NodeDetails are the node's facts: its ID, for support and the machine's
// own tools, and when it was linked. Its address stays on the Overview,
// hidden until revealed.
export function NodeDetails({ node }: { node: Node }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Details</CardTitle>
        <CardDescription>About this node, for support and the machine's own tools.</CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            [
              "Node ID",
              <code key="id" className="break-all">
                {node.id}
              </code>,
            ],
            ["Linked", node.createdAt ? dateOnly(timestampDate(node.createdAt)) : "—"],
          ]}
        />
      </CardContent>
    </Card>
  );
}

// NodeName renames a node (admins and owners).
export function NodeName({
  orgId,
  nodeId,
  name: current,
}: {
  orgId: string;
  nodeId: string;
  name: string;
}) {
  const client = useQueryClient();
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>Name</CardTitle>
        <CardDescription>
          What the node is called in Raptor. The machine's hostname follows it, unless you set one
          of its own on the System tab.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <form onSubmit={rename} className="flex items-end gap-2">
          <div className="flex flex-1 flex-col gap-1.5">
            <Label htmlFor="node-name" className="sr-only">
              Name
            </Label>
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
      </CardContent>
    </Card>
  );
}

// NodeRemove takes a node out of the org (admins and owners); it goes last
// on the page, like every action that can't be undone.
export function NodeRemove({
  orgId,
  nodeId,
  name,
}: {
  orgId: string;
  nodeId: string;
  name: string;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const withReauth = useReauth();
  const ask = useConfirm();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function remove() {
    const ok = await ask.confirm({
      title: `Remove ${name} from this org?`,
      description:
        "Its servers keep running on the machine, but you can't manage them here until it's linked again. It stops counting toward your plan.",
      typeToConfirm: name,
      confirm: "Remove node",
      destructive: true,
    });
    if (!ok) return;
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
        <CardTitle>Remove node</CardTitle>
        <CardDescription>
          Raptor stops managing it: its key is refused and its <code>raptornodes.net</code> name is
          removed. Its servers keep running on the machine; to stop them too, run{" "}
          <code>sudo raptor unlink</code> there first. A new join token links it again.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button variant="destructive" className="self-start" disabled={busy} onClick={remove}>
          Remove node
        </Button>
      </CardContent>
    </Card>
  );
}

// NodeSFTP allows or stops SFTP on the node. Its port opens only while
// someone has turned on SFTP for one of its servers (the Panel's gate).
export function NodeSFTP({ orgId, node }: { orgId: string; node: Node }) {
  const client = useQueryClient();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function set(allowed: boolean) {
    setBusy(true);
    setError("");
    try {
      await orgClient.setNodeSFTP({ orgId, nodeId: node.id, allowed });
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
        <CardTitle>SFTP</CardTitle>
        <CardDescription>
          Members with the SFTP permission can turn on a temporary login for a server from its Files
          tab. The port stays closed until someone does, and closes again when the last login is
          turned off or runs out.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-4">
          <label htmlFor="node-sftp" className="text-sm">
            <span className="block font-medium">Allow SFTP on this node</span>
            <span className="block text-xs text-muted-foreground">
              Stopping it closes the port and ends every login now.
            </span>
          </label>
          <Switch
            id="node-sftp"
            checked={node.sftpAllowed}
            disabled={busy}
            onCheckedChange={(v) => set(v)}
          />
        </div>
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <span
            className={cn(
              "size-2 rounded-full",
              node.sftpEnabled ? "bg-ok" : "bg-muted-foreground/40",
            )}
          />
          {node.sftpEnabled ? `Open now, on port ${node.sftpPort}` : "Closed: nobody is using SFTP"}
        </p>
        {node.sftpEnabled && node.sftpHostKeyFingerprint && (
          <p className="text-xs text-muted-foreground">
            Host key: <code>{node.sftpHostKeyFingerprint}</code>
          </p>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
