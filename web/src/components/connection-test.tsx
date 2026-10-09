import { useMutation } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  Info,
  Loader2,
  type LucideIcon,
  Radar,
  XCircle,
} from "lucide-react";
import { type ReactNode, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type Cause, diagnose, type Inside, type Verdict } from "@/lib/connection-test";
import { message } from "@/lib/errors";
import { commandClient, orgClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

type Result = {
  address: string;
  provider: string;
  reverseDns: string;
  verdicts: Verdict[];
  insideKnown: boolean;
};

// ConnectionTest checks whether players can reach the server: the node
// says what the game listens on, and the Panel tries each port from the
// internet. When something's wrong it says what, and how to fix it.
export function ConnectionTest({
  orgId,
  nodeId,
  serverId,
  host,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  host: string;
}) {
  const test = useMutation({
    mutationFn: async (): Promise<Result> => {
      const [outside, inside] = await Promise.all([
        orgClient.testConnection({ orgId, nodeId, serverId }),
        commandClient
          .execute({ nodeId, serverId, action: "server.ports", paramsJson: "{}" })
          .then((r) => JSON.parse(r.resultJson) as Inside)
          .catch(() => undefined), // an older Wings, or offline: outside only
      ]);
      return {
        address: outside.address,
        provider: outside.provider,
        reverseDns: outside.reverseDns,
        insideKnown: inside !== undefined,
        verdicts: diagnose(
          outside.ports.map((p) => ({
            port: p.port,
            primary: p.primary,
            reachable: p.reachable,
            failure: p.failure,
          })),
          inside,
        ),
      };
    },
  });
  const r = test.data;
  const primary = r?.verdicts[0];
  const blocked = r?.verdicts.filter((v) => v.cause === "blocked" || v.cause === "ok_udp") ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle>Connection test</CardTitle>
          <CardDescription>
            Checks from the internet whether players can reach this server, and if not, why.
          </CardDescription>
        </div>
        <Button size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? <Loader2 className="animate-spin" /> : <Radar />}
          {test.isPending ? "Testing…" : r ? "Test again" : "Test connection"}
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {test.error && <p className="text-sm text-destructive">{message(test.error)}</p>}
        {test.isPending && (
          <p className="text-sm text-muted-foreground">
            Connecting to {host} from the internet. This takes up to 5 seconds.
          </p>
        )}
        {r && !r.address && (
          <Row
            icon={Info}
            tone="muted"
            title="The node's public address isn't known yet"
            text="It's learned when the node connects. Try again in a minute."
          />
        )}
        {r?.address && r.verdicts.length === 0 && (
          <Row icon={Info} tone="muted" title="This server has no ports" text="Nothing to test." />
        )}
        {r && primary && (
          <>
            <Summary v={primary} host={host} />
            {r.verdicts.length > 1 && (
              <ul className="flex flex-col gap-1.5 text-sm">
                {r.verdicts.map((v) => (
                  <li key={v.port} className="flex items-center gap-2">
                    <StatusIcon cause={v.cause} className="size-4" />
                    <code className="text-xs">{v.port}</code>
                    <span className="text-muted-foreground">
                      {v.primary ? "Game port · " : ""}
                      {short[v.cause]}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {!r.insideKnown && (
              <p className="text-xs text-muted-foreground">
                The node didn't say what the game listens on (it may need a Wings update), so this
                is from outside only.
              </p>
            )}
            {blocked.length > 0 && (
              <Guide detected={r.provider} address={r.address} ports={blocked.map((v) => v.port)} />
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

const short: Record<Cause, string> = {
  ok: "Reachable",
  ok_udp: "UDP: listening; can't be tested from outside",
  not_running: "Server not running",
  starting: "Nothing listening yet",
  wrong_port: "The game listens on another port",
  loopback: "The game only listens on localhost",
  blocked: "Blocked before it reaches the node",
  local_only: "Local only (127.0.0.1), on purpose",
  unknown: "Couldn't tell",
};

function StatusIcon({ cause, className }: { cause: Cause; className?: string }) {
  if (cause === "ok") return <CheckCircle2 className={cn("text-emerald-500", className)} />;
  if (cause === "ok_udp" || cause === "local_only" || cause === "unknown")
    return <Info className={cn("text-muted-foreground", className)} />;
  if (cause === "not_running" || cause === "starting")
    return <AlertTriangle className={cn("text-amber-500", className)} />;
  return <XCircle className={cn("text-destructive", className)} />;
}

function Row({
  icon: Icon,
  tone,
  title,
  text,
}: {
  icon: LucideIcon;
  tone: "ok" | "warn" | "bad" | "muted";
  title: string;
  text: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex gap-3 rounded-lg border p-3 text-sm",
        tone === "ok" && "border-emerald-500/30 bg-emerald-500/5",
        tone === "warn" && "border-amber-500/40 bg-amber-500/5",
        tone === "bad" && "border-destructive/40 bg-destructive/5",
      )}
    >
      <Icon
        className={cn(
          "mt-0.5 size-4 shrink-0",
          tone === "ok" && "text-emerald-500",
          tone === "warn" && "text-amber-500",
          tone === "bad" && "text-destructive",
          tone === "muted" && "text-muted-foreground",
        )}
      />
      <div className="min-w-0 space-y-1">
        <p className="font-medium">{title}</p>
        <div className="text-muted-foreground">{text}</div>
      </div>
    </div>
  );
}

function Summary({ v, host }: { v: Verdict; host: string }) {
  const at = <code className="text-foreground">{`${host}:${v.port}`}</code>;
  switch (v.cause) {
    case "ok":
      return (
        <Row
          icon={CheckCircle2}
          tone="ok"
          title="Players can reach this server"
          text={<>At {at}.</>}
        />
      );
    case "ok_udp":
      return (
        <Row
          icon={Info}
          tone="muted"
          title="The game is listening, over UDP"
          text={
            <>
              UDP can't be tested from outside without the game answering. If players can't join at{" "}
              {at}, the port is probably blocked by your provider's firewall: open it for UDP using
              the guide below.
            </>
          }
        />
      );
    case "not_running":
      return (
        <Row
          icon={AlertTriangle}
          tone="warn"
          title="The server isn't running"
          text="Start it, wait until it's up, then test again."
        />
      );
    case "starting":
      return (
        <Row
          icon={AlertTriangle}
          tone="warn"
          title={`Nothing is listening on port ${v.port} yet`}
          text="If the server just started, give it a minute and test again. If it's been running a while, check its console for errors."
        />
      );
    case "wrong_port":
      return (
        <Row
          icon={XCircle}
          tone="bad"
          title={`The game is listening on port ${v.others.join(", ")}, not ${v.port}`}
          text={
            <>
              Players connect to port {v.port}, but the game opened {v.others.join(", ")}. Set the
              port in its config to {v.port} (for Minecraft, <code>server-port</code> in{" "}
              <code>server.properties</code>), then restart it.
            </>
          }
        />
      );
    case "loopback":
      return (
        <Row
          icon={XCircle}
          tone="bad"
          title="The game only accepts connections from its own machine"
          text={
            <>
              It's listening on 127.0.0.1. Clear the address it binds to (for Minecraft, leave{" "}
              <code>server-ip</code> in <code>server.properties</code> empty), then restart it.
            </>
          }
        />
      );
    case "blocked":
      return (
        <Row
          icon={XCircle}
          tone="bad"
          title="Players can't reach this server"
          text={
            <>
              The game is listening, but connections to {at} don't get through. Something in front
              of the node is blocking port {v.port}, almost always your provider's firewall. Open it
              with the guide below.
            </>
          }
        />
      );
    case "local_only":
      return (
        <Row
          icon={Info}
          tone="muted"
          title="This port is local only"
          text="It's on 127.0.0.1, so only other servers on the node reach it. That's on purpose."
        />
      );
    default:
      return (
        <Row
          icon={Info}
          tone="muted"
          title="Couldn't tell"
          text="The port didn't answer, and the node didn't say why. Make sure the server is running, then test again."
        />
      );
  }
}

// Each provider's steps, with the ports filled in.
const guides: { id: string; name: string; steps: (p: string, proto: string) => ReactNode[] }[] = [
  {
    id: "hetzner",
    name: "Hetzner",
    steps: (p, proto) => [
      <>
        <b>Cloud:</b> Firewalls → your firewall → Inbound rules → Add rule: {proto}, port {p},
        source Any IPv4 and Any IPv6. Make sure the firewall is applied to this server.
      </>,
      <>
        <b>Dedicated (Robot):</b> your server → Firewall → add a rule allowing {proto} port {p}, and
        save.
      </>,
    ],
  },
  {
    id: "ovh",
    name: "OVHcloud",
    steps: (p, proto) => [
      <>OVH doesn't block ports by default.</>,
      <>
        If you turned on the Edge Network Firewall (Network → Public IP addresses → your IP → … →
        Edge Network Firewall configuration), add an Authorize rule for {proto} port {p} before any
        deny rule.
      </>,
    ],
  },
  {
    id: "aws",
    name: "AWS",
    steps: (p, proto) => [
      <>EC2 → Instances → this instance → Security → its security group → Edit inbound rules.</>,
      <>
        Add a rule for each of {proto}: port {p}, source 0.0.0.0/0 (and ::/0 for IPv6). Save.
      </>,
    ],
  },
  {
    id: "gcp",
    name: "Google Cloud",
    steps: (p, proto) => [
      <>VPC network → Firewall → Create firewall rule.</>,
      <>
        Direction ingress, targets this VM (all instances, or its network tag), source 0.0.0.0/0,
        protocols {proto} port {p}. Create.
      </>,
    ],
  },
  {
    id: "azure",
    name: "Azure",
    steps: (p) => [
      <>This VM → Networking → Add inbound port rule.</>,
      <>Destination port ranges {p}, protocol Any, action Allow. Add.</>,
    ],
  },
  {
    id: "oracle",
    name: "Oracle Cloud",
    steps: (p, proto) => [
      <>
        Networking → Virtual cloud networks → your VCN → Security Lists → the default list → Add
        Ingress Rules: source 0.0.0.0/0, {proto}, destination port {p}.
      </>,
      <>
        Oracle's own images also come with firewall rules on the machine. If it still fails, run{" "}
        <code>sudo raptor doctor</code> on the node: it says what to change.
      </>,
    ],
  },
  {
    id: "digitalocean",
    name: "DigitalOcean",
    steps: (p, proto) => [
      <>
        Networking → Firewalls. If a firewall applies to this droplet, add inbound rules for {proto}{" "}
        port {p} from all IPv4 and IPv6. Droplets without one have nothing blocked.
      </>,
    ],
  },
  {
    id: "linode",
    name: "Akamai (Linode)",
    steps: (p, proto) => [
      <>
        This Linode → Network → Firewalls. In its firewall, add inbound rules accepting {proto} port{" "}
        {p} from all addresses.
      </>,
    ],
  },
  {
    id: "vultr",
    name: "Vultr",
    steps: (p, proto) => [
      <>
        Network → Firewall → the group this server is in → add IPv4 (and IPv6) rules for {proto}{" "}
        port {p}, source anywhere.
      </>,
    ],
  },
  {
    id: "home",
    name: "A home connection",
    steps: (p, proto) => [
      <>
        Open your router's settings page (often <code>192.168.0.1</code> or <code>192.168.1.1</code>
        ) and find Port Forwarding (sometimes NAT, Virtual Server, or Gaming).
      </>,
      <>
        Forward {proto} port {p} to this machine's local address.
      </>,
      <>
        If your router's own internet address isn't the one above, your provider shares one address
        between customers (CGNAT) and forwarding can't work: ask them for a public IP.
      </>,
    ],
  },
  {
    id: "other",
    name: "Another provider",
    steps: (p, proto) => [
      <>
        In your provider's control panel, look for Firewall, Security group, or Network rules, and
        allow {proto} port {p} from anywhere.
      </>,
      <>
        Firewalls on the node itself (ufw, firewalld) rarely matter: the ports of game servers go
        around them. If you're unsure, run <code>sudo raptor doctor</code> on the node.
      </>,
    ],
  },
];

function Guide({
  detected,
  address,
  ports,
}: {
  detected: string;
  address: string;
  ports: number[];
}) {
  const [id, setId] = useState(guides.some((g) => g.id === detected) ? detected : "other");
  const guide = guides.find((g) => g.id === id) ?? guides[guides.length - 1];
  const p = ports.join(", ");
  return (
    <div className="flex flex-col gap-3 rounded-lg border p-4 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="font-medium">How to open {ports.length === 1 ? "the port" : "the ports"}</p>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          Your node is on
          <select className={select} value={id} onChange={(e) => setId(e.target.value)}>
            {guides.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
                {g.id === detected ? " (detected)" : ""}
              </option>
            ))}
          </select>
        </label>
      </div>
      <ol className="flex list-decimal flex-col gap-2 pl-5">
        {/* Games use TCP, UDP, or both: open both, so the guide is right for any. */}
        {guide?.steps(p, "TCP and UDP").map((s, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list of steps
          <li key={i}>{s}</li>
        ))}
      </ol>
      <p className="text-xs text-muted-foreground">
        Then test again. The node's public address is <code>{address}</code>.
      </p>
    </div>
  );
}
