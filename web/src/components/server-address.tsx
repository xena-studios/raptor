import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Globe } from "lucide-react";
import { type FormEvent, useMemo, useState } from "react";

import { CopyButton, Hidden } from "@/components/address";
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
import { type DnsRecord, domainName, domainRecords, subdomainLabel } from "@/lib/addresses";
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
  ipv4,
  port,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  current: string;
  // The node's hostname and the server's primary port, for the records
  // "Use your own domain" shows.
  host: string;
  ipv4: string;
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
          ipv4={ipv4}
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

// OwnDomain teaches the owner to point a domain they have at this
// server: at a subdomain (a CNAME to the node's hostname) or at the domain
// itself, and for Minecraft Java an SRV record so players needn't type the
// port. It shows the exact records, with copy buttons. Raptor changes no
// DNS itself (docs: help/your-own-domain).
function OwnDomain({
  host,
  ipv4,
  port,
  srv,
  busy,
  onClose,
  onUse,
}: {
  host: string;
  ipv4: string;
  port: number;
  srv: boolean;
  busy: boolean;
  onClose: () => void;
  onUse: (value: string) => Promise<void>;
}) {
  const [domainInput, setDomainInput] = useState("");
  const [atRoot, setAtRoot] = useState(false);
  const [subInput, setSubInput] = useState("play");
  const domain = domainName(domainInput);
  const sub = atRoot ? "" : subdomainLabel(subInput);
  const ready = domain !== "" && (atRoot || sub !== "") && (srv || !atRoot || ipv4 !== "");
  const { records, shown } = domainRecords({
    domain: domain || "example.com",
    sub: atRoot ? "" : sub || "play",
    host,
    ipv4,
    port,
    srv,
  });
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Use your own domain</DialogTitle>
          <DialogDescription>
            Add these records where your domain's DNS is managed (often where you bought it, or
            Cloudflare). Raptor doesn't need access to your domain.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-5">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="own-domain">Your domain</Label>
            <Input
              id="own-domain"
              placeholder="example.com"
              autoComplete="off"
              spellCheck={false}
              maxLength={253}
              value={domainInput}
              onChange={(e) => setDomainInput(e.target.value)}
            />
            {domainInput.trim() !== "" && !domain && (
              <p className="text-sm text-destructive">That isn't a domain name.</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label>Where players connect</Label>
            <div className="flex flex-wrap gap-1.5">
              <Button
                size="sm"
                variant={!atRoot ? "secondary" : "ghost"}
                aria-pressed={!atRoot}
                onClick={() => setAtRoot(false)}
              >
                A subdomain
              </Button>
              <Button
                size="sm"
                variant={atRoot ? "secondary" : "ghost"}
                aria-pressed={atRoot}
                onClick={() => setAtRoot(true)}
              >
                The domain itself (@)
              </Button>
            </div>
            {!atRoot ? (
              <div className="flex items-center gap-1.5">
                <Input
                  aria-label="Subdomain"
                  className="max-w-40"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={63}
                  value={subInput}
                  onChange={(e) => setSubInput(e.target.value)}
                />
                <span className="truncate text-sm text-muted-foreground">
                  .{domain || "example.com"}
                </span>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                {srv
                  ? "Players type just your domain. With an SRV record that's the only record needed, so a website on the domain keeps working."
                  : "A domain itself can't have a CNAME, so it points at the node's IP address. If your domain has a website, this replaces it: use a subdomain instead."}
              </p>
            )}
            {!atRoot && subInput.trim() !== "" && !sub && (
              <p className="text-sm text-destructive">
                Use letters, numbers, and hyphens, like play or mc.
              </p>
            )}
          </div>
          {records.map((r, i) => (
            <RecordCard key={r.type} step={i + 1} record={r} />
          ))}
          {atRoot && !srv && (
            <p className="text-sm text-muted-foreground">
              {ipv4
                ? "If the machine's IP address changes (a home connection, a new provider), update this record. On Cloudflare you can use a CNAME to the node's address on @ instead (Cloudflare flattens it), which follows the IP for you; some providers call this an ALIAS or ANAME record."
                : "The Panel hasn't seen this node's public IPv4 address, so there's no IP to point at. Use a subdomain instead."}
            </p>
          )}
          {srv && <SrvHelp />}
          <p className="text-sm text-muted-foreground">
            Players then connect to <code className="text-xs">{shown}</code>. Changes usually reach
            them within minutes.{" "}
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
          <Button disabled={busy || !ready} onClick={() => onUse(shown)}>
            Show {ready ? shown : "it"} to players
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const recordWhy: Record<DnsRecord["type"], string> = {
  CNAME:
    "Points the name at the node. The node's address follows the machine's IP, so this keeps working if it changes.",
  A: "Points the domain at the node's IP address.",
  SRV: "Tells Minecraft which address and port to use, so players type only the name.",
};

function RecordCard({ step, record: r }: { step: number; record: DnsRecord }) {
  const rows: [string, string, boolean][] =
    r.type === "SRV"
      ? [
          ["Name", r.name, false],
          ["Priority", String(r.priority), false],
          ["Weight", String(r.weight), false],
          ["Port", String(r.port), false],
          ["Target", r.target, r.private],
        ]
      : [
          ["Name", r.name, false],
          [r.type === "A" ? "IPv4 address" : "Target", r.value, r.private],
        ];
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-sm font-medium">
        {step}. Add {r.type === "A" ? "an" : "a"} {r.type} record
      </p>
      <p className="text-sm text-muted-foreground">{recordWhy[r.type]}</p>
      <dl className="divide-y rounded-lg border text-sm">
        <div className="flex items-center gap-3 px-3 py-1.5">
          <dt className="w-24 shrink-0 text-muted-foreground">Type</dt>
          <dd className="font-mono text-xs">{r.type}</dd>
        </div>
        {rows.map(([k, v, hidden]) => (
          <div key={k} className="flex items-center gap-3 px-3 py-1.5">
            <dt className="w-24 shrink-0 text-muted-foreground">{k}</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              {hidden ? (
                <Hidden value={v} label={k.toLowerCase()} />
              ) : (
                <>
                  <code className="min-w-0 truncate text-xs">{v}</code>
                  <CopyButton value={v} label={k.toLowerCase()} />
                </>
              )}
            </dd>
          </div>
        ))}
      </dl>
      {r.type === "CNAME" && (
        <p className="text-xs text-muted-foreground">
          On Cloudflare, set Proxy status to <strong>DNS only</strong> (grey cloud). With the proxy
          on, players can't connect.
        </p>
      )}
      {r.type === "SRV" && (
        <p className="text-xs text-muted-foreground">
          Some providers ask for the parts separately: Service <code>{r.service}</code>, Protocol{" "}
          <code>{r.protocol}</code>, Name <code>{r.host}</code>. The full name is{" "}
          <code>{r.full}</code>.
        </p>
      )}
    </div>
  );
}

// SrvHelp explains an SRV record's fields, for owners adding their first.
function SrvHelp() {
  return (
    <details className="rounded-lg border px-3 py-2 text-sm">
      <summary className="cursor-pointer font-medium">How an SRV record works</summary>
      <div className="mt-2 flex flex-col gap-2 text-muted-foreground">
        <p>
          When a player types your name, Minecraft first asks DNS for <code>_minecraft._tcp.</code>{" "}
          in front of it. If there's an SRV record, it connects to the record's target and port
          instead, so the server's port can be anything and players never see it.
        </p>
        <ul className="flex list-disc flex-col gap-1 pl-5">
          <li>
            <strong>Service and protocol</strong> (<code>_minecraft</code>, <code>_tcp</code>):
            which game the record is for. Always these for Minecraft Java.
          </li>
          <li>
            <strong>Priority</strong>: with several records, players try the lowest first. One
            record: 0.
          </li>
          <li>
            <strong>Weight</strong>: shares players between records of the same priority. One
            record: any number, like 5.
          </li>
          <li>
            <strong>Port</strong>: the server's port on the node.
          </li>
          <li>
            <strong>Target</strong>: a hostname, never an IP address. Use the node's address, which
            follows its IP.
          </li>
        </ul>
        <p>
          Only Minecraft Java and its proxies (Velocity, BungeeCord) read SRV records. Bedrock and
          most other games need the port typed.
        </p>
      </div>
    </details>
  );
}
