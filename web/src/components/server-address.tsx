import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Globe } from "lucide-react";
import { type FormEvent, useMemo, useState } from "react";

import { CopyButton } from "@/components/address";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { domainName } from "@/lib/addresses";
import { message } from "@/lib/errors";
import { docs } from "@/lib/help";
import type { ServerConfig } from "@/lib/servers";
import { orgClient } from "@/lib/transport";

// ShownAddress sets a server's shown address: what players connect to
// instead of its node's hostname (a proxy's domain, or the owner's own), so
// the machine's address isn't on screen. Admins and owners.
export function ShownAddress({
  orgId,
  nodeId,
  serverId,
  current,
  host,
  port,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  current: string;
  // The node's hostname and the server's primary port, for the records
  // "Use your own domain" shows.
  host: string;
  port?: number;
}) {
  const client = useQueryClient();
  const [addr, setAddr] = useState(current);
  const [domainOpen, setDomainOpen] = useState(false);
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  // Minecraft Java (and its proxies) read SRV records; the egg's player
  // query says which servers speak its protocol.
  const srv = useMemo(() => {
    if (!res.data?.configJson) return false;
    return (JSON.parse(res.data.configJson) as ServerConfig).egg?.players === "minecraft";
  }, [res.data?.configJson]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent, value = addr) {
    e.preventDefault();
    await set(value);
  }

  async function set(value: string) {
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
        {host && port !== undefined && (
          <Button
            type="button"
            variant="link"
            className="h-auto self-start p-0"
            onClick={() => setDomainOpen(true)}
          >
            <Globe />
            Use your own domain
          </Button>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      {domainOpen && port !== undefined && (
        <OwnDomain
          host={host}
          port={port}
          srv={srv}
          busy={busy}
          onClose={() => setDomainOpen(false)}
          onUse={async (value) => {
            await set(value);
            setDomainOpen(false);
          }}
        />
      )}
    </Card>
  );
}

// OwnDomain shows the DNS records that point a name the owner owns at
// this server: a CNAME to the node's hostname, and for Minecraft Java an
// SRV record so players need no port. Raptor changes no DNS itself; the
// owner adds the records at their DNS provider (docs: help/your-own-domain).
function OwnDomain({
  host,
  port,
  srv,
  busy,
  onClose,
  onUse,
}: {
  host: string;
  port: number;
  srv: boolean;
  busy: boolean;
  onClose: () => void;
  onUse: (value: string) => Promise<void>;
}) {
  const [input, setInput] = useState("");
  const name = domainName(input);
  // A name with only two parts (example.com) is the bare domain, where a
  // CNAME isn't allowed. Public suffixes like co.uk make this a guess, so
  // it's a note, not an error.
  const bare = name !== "" && name.split(".").length === 2;
  // Where a provider splits an SRV record's name, the part under the zone:
  // usually the first label (play), or @ for the bare domain.
  const sub = name.split(".").length > 2 ? name.split(".")[0] : "@";
  const shown = srv ? name : `${name}:${port}`;
  const rows: [string, string][] = [
    ["Type", "CNAME"],
    ["Name", name || "play.example.com"],
    ["Target", host],
  ];
  const srvRows: [string, string][] = [
    ["Type", "SRV"],
    ["Name", name ? `_minecraft._tcp.${name}` : "_minecraft._tcp.play.example.com"],
    ["Priority", "0"],
    ["Weight", "5"],
    ["Port", String(port)],
    ["Target", host],
  ];
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Use your own domain</DialogTitle>
          <DialogDescription>
            Add {srv ? "these records" : "this record"} where your domain's DNS is managed. Raptor
            doesn't need access to your domain.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="own-domain">The name players will type</Label>
            <Input
              id="own-domain"
              placeholder="play.example.com"
              autoComplete="off"
              spellCheck={false}
              maxLength={253}
              value={input}
              onChange={(e) => setInput(e.target.value)}
            />
            {input.trim() !== "" && !name && (
              <p className="text-sm text-destructive">That isn't a domain name.</p>
            )}
            {bare && (
              <p className="text-sm text-muted-foreground">
                Most DNS providers don't allow a CNAME on the bare domain. Use a name under it, like
                play.{name}.
              </p>
            )}
          </div>
          <Records title="Point the name at the node" rows={rows} />
          <p className="text-sm text-muted-foreground">
            On Cloudflare, set it to <strong>DNS only</strong> (grey cloud). With the proxy on,
            players can't connect.
          </p>
          {srv && (
            <Records
              title="So players don't need the port (Minecraft Java)"
              rows={srvRows}
              note={
                name
                  ? `Some providers split the name into Service _minecraft, Protocol _tcp, and Name ${sub}.`
                  : undefined
              }
            />
          )}
          <p className="text-sm text-muted-foreground">
            Changes usually reach players within minutes.{" "}
            <a
              href={docs("/help/your-own-domain/")}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 underline underline-offset-4"
            >
              Step-by-step guide
              <ExternalLink className="size-3.5" />
            </a>
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Close
          </Button>
          <Button disabled={busy || !name} onClick={() => onUse(shown)}>
            Show {name ? shown : "it"} to players
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Records({
  title,
  rows,
  note,
}: {
  title: string;
  rows: [string, string][];
  note?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-sm font-medium">{title}</p>
      <dl className="divide-y rounded-lg border text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center gap-3 px-3 py-1.5">
            <dt className="w-16 shrink-0 text-muted-foreground">{k}</dt>
            <dd className="min-w-0 flex-1 truncate font-mono text-xs">{v}</dd>
            {k !== "Type" && <CopyButton value={v} label={k.toLowerCase()} />}
          </div>
        ))}
      </dl>
      {note && <p className="text-xs text-muted-foreground">{note}</p>}
    </div>
  );
}
