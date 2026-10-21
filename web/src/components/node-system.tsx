import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import {
  CheckCircle2,
  ChevronRight,
  Loader2,
  Lock,
  Plus,
  Power,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { dbExternalCapability, useInstances } from "@/lib/databases";
import { message } from "@/lib/errors";
import { shortId } from "@/lib/ids";
import { sendSigned } from "@/lib/signed";
import { commandClient, orgClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";
import { timeZones } from "@/lib/zones";

// The machine's own settings, from the node's System tab: its hostname,
// time zone and clock, firewall, and rebooting it (Wings' node.* system
// actions; firewall changes and reboots are signed by a passkey).

type Rule = {
  action: "allow" | "block";
  protocol: "tcp" | "udp" | "both";
  ports: string;
  source: string;
  note: string;
};

type SystemState = {
  hostname: string;
  timezone: string;
  ntp: boolean;
  synced: boolean;
  firewall: { block_incoming: boolean; rules: Rule[] };
  open: { protocol: string; port: number; why: string }[] | null;
  ssh_ports: number[];
  ufw: boolean;
  ruleset?: Listed[];
  ruleset_truncated?: boolean;
  ruleset_error?: string;
};

// Listed is one rule on the machine, whoever's (Wings' firewall.Listed).
type Listed = {
  table: string;
  chain: string;
  hook?: string;
  rule: string;
  policy?: boolean;
  owner: "raptor" | "yours" | "docker" | "ufw" | "fail2ban" | "other";
  needed: boolean;
  why: string;
  index?: number;
  action?: "allow" | "block";
  protocol?: "tcp" | "udp" | "both";
  ports?: string;
  source?: string;
};

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

async function run<T>(nodeId: string, action: string, params: unknown = {}): Promise<T> {
  const res = await commandClient.execute({ nodeId, action, paramsJson: JSON.stringify(params) });
  return (res.resultJson ? JSON.parse(res.resultJson) : null) as T;
}

export function NodeSystem({ orgId, node, userId }: { orgId: string; node: Node; userId: string }) {
  const nodeId = node.id;
  const connected = node.connected;
  const state = useQuery({
    queryKey: ["node-system", nodeId],
    queryFn: () => run<SystemState>(nodeId, "node.system"),
    enabled: connected,
  });
  if (!connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          The node is offline, so its settings can't be read or changed until it's back.
        </CardContent>
      </Card>
    );
  }
  if (state.error) return <p className="text-sm text-destructive">{message(state.error)}</p>;
  const s = state.data;
  if (!s) return <p className="text-sm text-muted-foreground">Asking the node…</p>;
  return (
    <div className="flex flex-col gap-6">
      <Machine
        key={`${s.hostname}/${s.timezone}/${node.hostnameCustom}`}
        orgId={orgId}
        node={node}
        s={s}
      />
      <Firewall key={JSON.stringify(s.firewall)} orgId={orgId} node={node} userId={userId} s={s} />
      <Reboot nodeId={nodeId} userId={userId} />
    </div>
  );
}

// hostnameFor is the hostname a node's name makes, as the Panel makes it
// (machines.Want).
function hostnameFor(node: Node): string {
  const h = node.name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/, "");
  return h || `n-${node.shortId}`;
}

function Machine({ orgId, node, s }: { orgId: string; node: Node; s: SystemState }) {
  const nodeId = node.id;
  const client = useQueryClient();
  const follows = !node.hostnameCustom;
  const [hostname, setHostname] = useState(follows ? s.hostname : node.hostnameCustom);
  const [custom, setCustom] = useState(!follows);
  const [tz, setTz] = useState(s.timezone);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState<{ ok: boolean; text: string }>();
  const mine = Intl.DateTimeFormat().resolvedOptions().timeZone;

  async function change(what: string, fn: () => Promise<string | undefined>) {
    setBusy(what);
    setNotice(undefined);
    try {
      const note = await fn();
      setNotice({ ok: true, text: note || "Saved." });
      await client.invalidateQueries({ queryKey: ["node-system", nodeId] });
    } catch (err) {
      setNotice({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  function setHostnameTo(value: string) {
    change("hostname", async () => {
      const r = await orgClient.setNodeHostname({ orgId, nodeId, hostname: value });
      await client.invalidateQueries();
      return r.note;
    });
  }
  function saveHostname(e: FormEvent) {
    e.preventDefault();
    setHostnameTo(hostname);
  }
  function saveTz(e: FormEvent) {
    e.preventDefault();
    change("tz", async () => {
      await run(nodeId, "node.time", { timezone: tz });
      return "Saved. Servers use the new time zone the next time they start.";
    });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Machine</CardTitle>
        <CardDescription>
          The machine's own name and clock. Its <code>raptornodes.net</code> address doesn't change
          with the hostname.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-0.5">
              <label htmlFor="sys-follow" className="block text-sm font-medium">
                Hostname follows the node's name
              </label>
              <p className="text-xs text-muted-foreground">
                Renaming the node renames the machine: "{node.name}" is{" "}
                <code>{hostnameFor(node)}</code>.
              </p>
            </div>
            <Switch
              id="sys-follow"
              checked={!custom}
              disabled={!!busy}
              onCheckedChange={(on) => {
                setCustom(!on);
                if (on) {
                  setHostname(hostnameFor(node));
                  if (!follows) setHostnameTo("");
                }
              }}
            />
          </div>
          <form onSubmit={saveHostname} className="flex items-end gap-2">
            <div className="flex flex-1 flex-col gap-1.5">
              <Label htmlFor="sys-hostname">Hostname</Label>
              <Input
                id="sys-hostname"
                value={custom ? hostname : s.hostname}
                readOnly={!custom}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setHostname(e.target.value)}
              />
            </div>
            {custom && (
              <Button
                type="submit"
                variant="outline"
                disabled={
                  !!busy ||
                  !hostname.trim() ||
                  (hostname === node.hostnameCustom && hostname === s.hostname)
                }
              >
                {busy === "hostname" && <Loader2 className="animate-spin" />} Rename
              </Button>
            )}
          </form>
          {node.hostnameError && (
            <p className="text-xs text-destructive">
              The node refused the last hostname it was sent: {node.hostnameError}
            </p>
          )}
        </div>
        <form onSubmit={saveTz} className="flex flex-col gap-1.5">
          <Label htmlFor="sys-tz">Time zone</Label>
          <div className="flex items-end gap-2">
            <Input
              id="sys-tz"
              list="sys-tz-list"
              autoComplete="off"
              value={tz}
              onChange={(e) => setTz(e.target.value)}
              className="flex-1"
            />
            <datalist id="sys-tz-list">
              {timeZones().map((z) => (
                <option key={z} value={z} />
              ))}
            </datalist>
            <Button
              type="submit"
              variant="outline"
              disabled={!!busy || !tz.trim() || tz === s.timezone}
            >
              {busy === "tz" && <Loader2 className="animate-spin" />} Save
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            Servers get it as <code>TZ</code>; schedules keep their own.{" "}
            {tz !== mine && (
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => setTz(mine)}
              >
                Use yours ({mine})
              </button>
            )}
          </p>
        </form>
        <div className="flex items-center justify-between gap-4">
          <div className="space-y-0.5">
            <label htmlFor="sys-ntp" className="block text-sm font-medium">
              Keep the clock in sync
            </label>
            <p className="text-xs text-muted-foreground">
              {s.synced
                ? "In sync with internet time servers."
                : s.ntp
                  ? "Not in sync yet: it can take a few minutes after turning it on."
                  : "Off: the clock drifts, and backups to cloud storage fail when it's minutes off."}
            </p>
          </div>
          <Switch
            id="sys-ntp"
            checked={s.ntp}
            disabled={!!busy}
            onCheckedChange={(on) =>
              change("ntp", async () => {
                await run(nodeId, "node.time", { ntp: on });
                return undefined;
              })
            }
          />
        </div>
        {notice && (
          <p className={cn("text-sm", notice.ok ? "text-muted-foreground" : "text-destructive")}>
            {notice.text}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function portsText(ports: string | undefined, protocol: string | undefined) {
  if (!ports)
    return protocol && protocol !== "both" ? `Every ${protocol.toUpperCase()} port` : "Every port";
  return protocol && protocol !== "both" ? `${ports} ${protocol.toUpperCase()}` : ports;
}

function sourceText(source: string | undefined) {
  return source?.replace(/\/(32|128)$/, "") || "Anywhere";
}

const emptyRule: Rule = { action: "allow", protocol: "tcp", ports: "", source: "", note: "" };

// Opened is a port Raptor keeps open on the machine, listed the way
// `ufw status` lists a rule: what it opens, from where, and why. These
// follow from the node's own state (SSH, SFTP, servers' allocations,
// databases' external access), so they're changed where that state is,
// never here.
type Opened = { key: string; to: string; from: string; why: ReactNode };

function useOpened(orgId: string, node: Node, s: SystemState): Opened[] {
  const servers = useConnectQuery(OrgService.method.listServers, { orgId, nodeId: node.id });
  const external = node.capabilities.includes(dbExternalCapability);
  const instances = useInstances(node.id, external);
  const link = "underline underline-offset-2 hover:text-foreground";
  const out: Opened[] = [];
  for (const port of s.ssh_ports ?? []) {
    out.push({
      key: `ssh-${port}`,
      to: `${port}/tcp`,
      from: "Anywhere",
      why: "SSH, so you can always reach the machine. Raptor never blocks it, so a firewall change can't lock you out.",
    });
  }
  if (node.sftpEnabled && node.sftpPort) {
    out.push({
      key: "sftp",
      to: `${node.sftpPort}/tcp`,
      from: "Anywhere",
      why: (
        <>
          Raptor's SFTP. It's open only while someone has an SFTP password for this node, and closes
          when the last one expires. Turn SFTP off on the{" "}
          <Link
            to="/orgs/$orgId/nodes/$nodeId/settings"
            params={{ orgId, nodeId: node.id }}
            className={link}
          >
            Settings tab
          </Link>
          .
        </>
      ),
    });
  }
  for (const srv of servers.data?.servers ?? []) {
    if (srv.ports.length === 0) continue;
    out.push({
      key: `server-${srv.id}`,
      to: `${srv.ports.join(", ")}/tcp+udp`,
      from: "Anywhere",
      why: (
        <>
          Players connecting to{" "}
          <Link
            to="/orgs/$orgId/servers/$serverId/network"
            params={{ orgId, serverId: shortId(srv.id) }}
            className={link}
          >
            {srv.name}
          </Link>
          . Its ports open while it exists; change them on its Network tab.
        </>
      ),
    });
  }
  for (const i of instances.data?.instances ?? []) {
    if (!i.external) continue;
    out.push({
      key: `db-${i.id}`,
      to: `${i.external.port}/tcp`,
      from:
        i.external.allow.length === 1
          ? (i.external.allow[0] ?? "")
          : `${i.external.allow.length} addresses`,
      why: (
        <>
          External access to the database instance {i.name}, only from the addresses it allows.
          Change it on the{" "}
          <Link
            to="/orgs/$orgId/nodes/$nodeId/databases"
            params={{ orgId, nodeId: node.id }}
            className={link}
          >
            Databases tab
          </Link>
          .
        </>
      ),
    });
  }
  return out;
}

function Firewall({
  orgId,
  node,
  userId,
  s,
}: {
  orgId: string;
  node: Node;
  userId: string;
  s: SystemState;
}) {
  const nodeId = node.id;
  const client = useQueryClient();
  const ask = useConfirm();
  const opened = useOpened(orgId, node, s);
  const [block, setBlock] = useState(s.firewall.block_incoming);
  const [rules, setRules] = useState<Rule[]>(s.firewall.rules.map((r) => ({ ...emptyRule, ...r })));
  const [draft, setDraft] = useState<Rule>(emptyRule);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const changed =
    block !== s.firewall.block_incoming ||
    JSON.stringify(rules) !== JSON.stringify(s.firewall.rules.map((r) => ({ ...emptyRule, ...r })));

  function add(e: FormEvent) {
    e.preventDefault();
    if (!draft.ports.trim() && !draft.source.trim()) {
      setError("Give the rule ports, an address, or both.");
      return;
    }
    setError("");
    setRules([...rules, { ...draft, ports: draft.ports.trim(), source: draft.source.trim() }]);
    setDraft(emptyRule);
  }

  async function remove(i: number) {
    const r = rules[i];
    if (!r) return;
    const ok = await ask.confirm({
      title: "Remove this rule?",
      description: `${r.action === "block" ? "Block" : "Allow"} ${portsText(r.ports, r.protocol)} from ${sourceText(r.source)}${r.note ? ` (${r.note})` : ""}. It's gone from the node once you save.`,
      confirm: "Remove rule",
      destructive: true,
    });
    if (ok) setRules(rules.filter((_, j) => j !== i));
  }

  async function save() {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "node.firewall",
        params: { block_incoming: block, rules },
      });
      setSaved(true);
      await client.invalidateQueries({ queryKey: ["node-system", nodeId] });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Firewall</CardTitle>
        <CardDescription>
          What can reach this machine from the internet, listed like <code>ufw status</code>. Raptor
          opens what SSH, your servers, and SFTP need; add rules of your own to allow or block more.
          Which servers can reach each other is set on the{" "}
          <Link
            to="/orgs/$orgId/nodes/$nodeId/networks"
            params={{ orgId, nodeId }}
            className="underline underline-offset-2 hover:text-foreground"
          >
            Networks tab
          </Link>
          .
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        {s.ufw && (
          <p className="flex gap-2 rounded-lg border border-warn/40 bg-warn-wash p-3 text-sm">
            <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warn" />
            ufw is on too. It filters connections to the machine before these rules, so an address
            ufw blocks stays blocked whatever is allowed here. Game servers aren't affected.
          </p>
        )}

        <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
          <div className="space-y-1">
            <p className="font-mono text-xs tracking-wide uppercase">
              <span className="text-muted-foreground">Incoming: </span>
              <span className={block ? "text-ok" : "text-warn"}>{block ? "deny" : "allow"}</span>
              <span className="text-muted-foreground"> (default)</span>
            </p>
            <label htmlFor="fw-block" className="block text-sm font-medium">
              Block everything not listed
            </label>
            <p className="text-xs text-muted-foreground">
              {block
                ? "Connections to the machine that nothing below allows are dropped."
                : "The machine accepts connections on any port it listens on, not only those below. Turn this on unless something else on it needs to be reached."}
            </p>
          </div>
          <Switch id="fw-block" checked={block} onCheckedChange={setBlock} />
        </div>

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>To</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>From</TableHead>
              <TableHead className="hidden md:table-cell">Why</TableHead>
              <TableHead className="w-10">
                <span className="sr-only">Remove</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <GroupRow>Opened by Raptor</GroupRow>
            {opened.map((o) => (
              <TableRow key={o.key}>
                <TableCell className="font-mono text-xs">{o.to}</TableCell>
                <TableCell>
                  <ActionBadge action="allow" />
                </TableCell>
                <TableCell className="font-mono text-xs">{o.from}</TableCell>
                <TableCell className="hidden max-w-md text-xs whitespace-normal text-muted-foreground md:table-cell">
                  {o.why}
                </TableCell>
                <TableCell>
                  <span
                    className="flex size-7 items-center justify-center text-muted-foreground"
                    title="Raptor keeps this open: it's changed where it comes from, not here."
                  >
                    <Lock className="size-3.5" />
                    <span className="sr-only">Kept open by Raptor</span>
                  </span>
                </TableCell>
              </TableRow>
            ))}

            <GroupRow>Your rules</GroupRow>
            {rules.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={5} className="text-xs text-muted-foreground">
                  None yet. Add one below to open another port, or to block an address.
                </TableCell>
              </TableRow>
            )}
            {rules.map((r, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: rules have no IDs; order is their identity
              <TableRow key={i}>
                <TableCell className="font-mono text-xs">
                  {portsText(r.ports, r.protocol)}
                </TableCell>
                <TableCell>
                  <ActionBadge action={r.action} />
                </TableCell>
                <TableCell className="font-mono text-xs">{sourceText(r.source)}</TableCell>
                <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                  {r.note || "—"}
                </TableCell>
                <TableCell>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label="Remove rule"
                    onClick={() => remove(i)}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>

        <form onSubmit={add} className="grid gap-2 sm:grid-cols-[auto_1fr_auto_1fr_1fr_auto]">
          <select
            aria-label="Allow or block"
            className={select}
            value={draft.action}
            onChange={(e) => setDraft({ ...draft, action: e.target.value as Rule["action"] })}
          >
            <option value="allow">Allow</option>
            <option value="block">Block</option>
          </select>
          <Input
            aria-label="Ports"
            placeholder="Ports: 8080, 8000-8100"
            value={draft.ports}
            onChange={(e) => setDraft({ ...draft, ports: e.target.value })}
          />
          <select
            aria-label="Protocol"
            className={select}
            value={draft.protocol}
            onChange={(e) => setDraft({ ...draft, protocol: e.target.value as Rule["protocol"] })}
          >
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="both">TCP and UDP</option>
          </select>
          <Input
            aria-label="From"
            placeholder="From: an address, or anywhere"
            value={draft.source}
            onChange={(e) => setDraft({ ...draft, source: e.target.value })}
          />
          <Input
            aria-label="Note"
            placeholder="Note (optional)"
            maxLength={100}
            value={draft.note}
            onChange={(e) => setDraft({ ...draft, note: e.target.value })}
          />
          <Button type="submit" variant="outline">
            <Plus /> Add
          </Button>
        </form>

        <div className="flex flex-wrap items-center gap-3">
          <Button onClick={save} disabled={busy || !changed}>
            {busy && <Loader2 className="animate-spin" />} Save with passkey
          </Button>
          {changed && !busy && (
            <span className="text-sm text-muted-foreground">Not saved yet.</span>
          )}
          {saved && !changed && (
            <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <CheckCircle2 className="size-4 text-ok" /> Saved and loaded on the node.
            </span>
          )}
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <MachineRules s={s} />
      </CardContent>
    </Card>
  );
}

// ActionBadge is a rule's action: allow in the OK colour, block in danger,
// as state colours (DESIGN.md: colour carries state).
function ActionBadge({ action }: { action: "allow" | "block" }) {
  return action === "block" ? (
    <Badge variant="destructive">Block</Badge>
  ) : (
    <Badge className="bg-ok-wash text-ok">Allow</Badge>
  );
}

function GroupRow({ children }: { children: ReactNode }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell
        colSpan={5}
        className="pt-4 font-mono text-[11px] tracking-wide text-muted-foreground uppercase"
      >
        {children}
      </TableCell>
    </TableRow>
  );
}

const owners: Record<Exclude<Listed["owner"], "yours">, { name: string; about: string }> = {
  ufw: { name: "ufw", about: "ufw is on. Change its rules with the ufw command." },
  fail2ban: { name: "fail2ban", about: "Blocks for addresses that keep failing to log in." },
  other: {
    name: "Something else",
    about:
      "Not from Raptor or Docker: your provider's image, a script, or another program. Raptor doesn't change them.",
  },
  raptor: {
    name: "Raptor",
    about:
      "Keeps install containers off your network and the cloud metadata service, and sends connections through your rules. Raptor puts them back if they're removed.",
  },
  docker: {
    name: "Docker",
    about:
      "Servers run in Docker containers: these send players to them and let them reach the internet. Docker keeps them up to date.",
  },
};

// The order the machine's rules are listed in, after the owner's own.
const ownerOrder = Object.keys(owners) as (keyof typeof owners)[];

// MachineRules is the rest of the machine's firewall (nft list ruleset,
// docs/WINGS.md#firewall): rules other programs put there, folded away
// under the list above since Raptor doesn't change them. Allow and block
// rules read by their ports and source, anything else as nftables prints
// it. It opens by itself when something there drops traffic Raptor's
// rules would let in, since that's what explains a port that won't open.
function MachineRules({ s }: { s: SystemState }) {
  const list = (s.ruleset ?? [])
    .filter((l): l is Listed & { owner: keyof typeof owners } => l.owner !== "yours")
    .map((l, i) => ({ ...l, i }))
    .sort((a, b) => ownerOrder.indexOf(a.owner) - ownerOrder.indexOf(b.owner) || a.i - b.i);
  const blocking = list.some(
    (l) => l.owner !== "raptor" && l.owner !== "docker" && (l.policy || l.action === "block"),
  );
  const [open, setOpen] = useState(blocking);
  const [all, setAll] = useState(false);
  if (list.length === 0 && !s.ruleset_error) return null;
  const plumbing = (l: Listed) => !l.action && !l.policy && l.owner !== "other";
  const folded = list.filter(plumbing).length;
  const shown = all ? list : list.filter((l) => !plumbing(l));
  return (
    <div className="flex flex-col gap-3 border-t pt-4">
      <Button
        variant="ghost"
        size="sm"
        className="self-start"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <ChevronRight className={cn("transition-transform", open && "rotate-90")} />
        Other rules on the machine ({list.length})
      </Button>
      {open && (
        <>
          <p className="text-xs text-muted-foreground">
            From Docker, ufw, fail2ban, and anything else on the machine. Raptor doesn't change
            these; those marked <Badge variant="outline">Needed</Badge> keep servers working.
          </p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Rule</TableHead>
                <TableHead>To</TableHead>
                <TableHead>From</TableHead>
                <TableHead className="hidden sm:table-cell">Set by</TableHead>
                <TableHead className="hidden md:table-cell">Why</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((l) => (
                <TableRow key={l.i} className="text-muted-foreground">
                  <TableCell>
                    {l.action ? (
                      <ActionBadge action={l.action} />
                    ) : l.policy ? (
                      <Badge variant="destructive">Drop the rest</Badge>
                    ) : (
                      <Badge variant="secondary">Other</Badge>
                    )}
                  </TableCell>
                  {l.action ? (
                    <>
                      <TableCell className="font-mono text-xs">
                        {portsText(l.ports, l.protocol)}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{sourceText(l.source)}</TableCell>
                    </>
                  ) : (
                    <TableCell
                      colSpan={2}
                      className="min-w-48 font-mono text-xs whitespace-normal break-all"
                    >
                      {l.policy ? `${l.chain} (${l.hook ?? "chain"})` : l.rule}
                    </TableCell>
                  )}
                  <TableCell className="hidden sm:table-cell" title={owners[l.owner].about}>
                    <span className="flex flex-wrap items-center gap-1.5">
                      {owners[l.owner].name}
                      {l.needed && <Badge variant="outline">Needed</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="hidden max-w-xs whitespace-normal md:table-cell">
                    <span className="block text-xs">{l.why}</span>
                    <span className="block font-mono text-[11px] text-muted-foreground/70">
                      {l.table} › {l.chain}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {folded > 0 && (
            <Button size="sm" variant="ghost" className="self-start" onClick={() => setAll(!all)}>
              <ChevronRight className={cn("transition-transform", all && "rotate-90")} />
              {all
                ? "Hide the rules that only route traffic"
                : `Show ${folded} more ${folded === 1 ? "rule" : "rules"} that only route traffic`}
            </Button>
          )}
          <RulesetNotes s={s} />
        </>
      )}
    </div>
  );
}

function RulesetNotes({ s }: { s: SystemState }) {
  if (!s.ruleset && !s.ruleset_error) return null; // an older Wings
  return (
    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
      {s.ruleset_error && <p className="text-sm text-destructive">{s.ruleset_error}</p>}
      {s.ruleset_truncated && (
        <p>
          Only the first {s.ruleset?.length} rules on the machine are listed. Run{" "}
          <code>sudo nft list ruleset</code> on it for all of them.
        </p>
      )}
      <p>
        Rules loaded with the older iptables-legacy tools aren't listed: run{" "}
        <code>sudo iptables-legacy -S</code> to see them.
      </p>
    </div>
  );
}

function Reboot({ nodeId, userId }: { nodeId: string; userId: string }) {
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ ok: boolean; text: string }>();

  async function reboot() {
    const ok = await ask.confirm({
      title: "Reboot the machine?",
      description:
        "Its servers stop properly first, and those that were running start again once it's back, usually within a few minutes.",
      confirm: "Reboot with passkey",
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    setNotice(undefined);
    try {
      await sendSigned({ userId, nodeId, action: "node.reboot" });
      setNotice({
        ok: true,
        text: "Rebooting. The node shows offline for a moment, then reconnects.",
      });
    } catch (err) {
      if (!passkeyCancelled(err)) setNotice({ ok: false, text: message(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Power</CardTitle>
        <CardDescription>
          Reboot after a kernel update, or when the machine misbehaves. Servers stop properly first.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <Button variant="outline" className="self-start" disabled={busy} onClick={reboot}>
          {busy ? <Loader2 className="animate-spin" /> : <Power />} Reboot with passkey
        </Button>
        {notice && (
          <p className={cn("text-sm", notice.ok ? "text-muted-foreground" : "text-destructive")}>
            {notice.text}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
