import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { Node } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

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

// NodeAddress sets the node's shown address: what players connect to
// instead of its hostname (a proxy's domain, or the owner's own), so the
// machine's address isn't on screen.
export function NodeAddress({ orgId, node }: { orgId: string; node: Node }) {
  const client = useQueryClient();
  const [addr, setAddr] = useState(node.displayAddress);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent, value = addr) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await orgClient.setNodeDisplayAddress({ orgId, nodeId: node.id, displayAddress: value });
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
          What players connect to, shown everywhere instead of the node's hostname. Use it for a
          proxy like TCPShield, or your own domain. The hostname and IP address stay hidden until
          you reveal them. With a port (play.example.com:25565), it's shown as is; without one, each
          server's port is added.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <form onSubmit={save} className="flex items-end gap-2">
          <div className="flex flex-1 flex-col gap-1.5">
            <Label htmlFor="node-address">Address</Label>
            <Input
              id="node-address"
              placeholder="play.example.com"
              autoComplete="off"
              spellCheck={false}
              maxLength={260}
              value={addr}
              onChange={(e) => setAddr(e.target.value)}
            />
          </div>
          <Button
            type="submit"
            variant="outline"
            disabled={busy || addr.trim() === node.displayAddress}
          >
            Save
          </Button>
          {node.displayAddress && (
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
