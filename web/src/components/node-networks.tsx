import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Loader2, Network, Pencil, Plus, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { NetworkRules } from "@/components/network-rules";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { shortId } from "@/lib/ids";
import {
  defaultNetworkName,
  type NetworkInput,
  networksKey,
  networkTraits,
  type RaptorNetwork,
  recreates,
  useNodeNetworks,
} from "@/lib/networks";
import { commandClient } from "@/lib/transport";

// The node's Networks tab (docs/NETWORKS.md): networks that keep groups of
// servers apart, the servers on each, and the machine's other Docker
// networks. Making one takes a name; the rest waits under Advanced.
export function NodeNetworks({
  orgId,
  nodeId,
  connected,
  supported,
  rulesSupported,
}: {
  orgId: string;
  nodeId: string;
  connected: boolean;
  // The node's Wings can have networks, and rules between them.
  supported: boolean;
  rulesSupported: boolean;
}) {
  const nets = useNodeNetworks(nodeId, connected && supported);
  const servers = useQuery(OrgService.method.listServers, { orgId, nodeId });
  const [editing, setEditing] = useState<RaptorNetwork | "new">();

  if (!supported) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          Update Wings on this node to manage its networks here.
        </CardContent>
      </Card>
    );
  }
  if (!connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          The node is offline, so its networks can't be read until it's back.
        </CardContent>
      </Card>
    );
  }
  if (nets.error) return <p className="text-sm text-destructive">{message(nets.error)}</p>;
  const d = nets.data;
  if (!d) return <p className="text-sm text-muted-foreground">Asking the node…</p>;

  const all = servers.data?.servers ?? [];
  const onSome = new Set(d.networks.flatMap((n) => n.servers));
  const byId = new Map(all.map((s) => [s.id, s]));
  const others = d.host.filter((h) => !h.raptor);

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Networks</CardTitle>
          <CardDescription>
            Servers on different networks can't reach each other, so communities sharing this
            machine stay apart. Players reach every server's ports either way.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setEditing("new")}>
              <Plus /> New network
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <NetworkRow
            orgId={orgId}
            name={defaultNetworkName}
            about="Where servers go unless you put them on another network."
            servers={all.filter((s) => !onSome.has(s.id))}
          />
          {d.networks.map((n) => (
            <NetworkRow
              key={n.id}
              orgId={orgId}
              name={n.name}
              traits={networkTraits(n)}
              about={
                <span className="font-mono">
                  {n.subnet}
                  {n.ipv6_subnet ? ` · ${n.ipv6_subnet}` : ""}
                </span>
              }
              servers={n.servers.map((id) => byId.get(id)).filter((s): s is Server => !!s)}
              addresses={n.addresses}
              actions={
                <>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Edit ${n.name}`}
                    onClick={() => setEditing(n)}
                  >
                    <Pencil />
                  </Button>
                  <DeleteButton nodeId={nodeId} network={n} />
                </>
              }
            />
          ))}
          {d.networks.length === 0 && (
            <p className="text-sm text-muted-foreground">
              Every server here can reach the others. Make a network for each group that should be
              kept apart, then move its servers onto it from their Network tab.
            </p>
          )}
        </CardContent>
      </Card>

      {rulesSupported && (
        <NetworkRules nodeId={nodeId} networks={d.networks} rules={d.rules ?? []} servers={byId} />
      )}

      {others.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Other Docker networks</CardTitle>
            <CardDescription>
              Networks on this machine Raptor didn't make, like Docker's own or another panel's.
              Raptor never changes them.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Driver</TableHead>
                  <TableHead>Subnet</TableHead>
                  <TableHead className="text-right">Containers</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {others.map((h) => (
                  <TableRow key={h.name}>
                    <TableCell className="font-medium">
                      {h.name}
                      {h.internal && (
                        <span className="ml-2 text-xs text-muted-foreground">No internet</span>
                      )}
                    </TableCell>
                    {/* Docker's "none" network reports its driver as "null". */}
                    <TableCell>{h.driver === "null" ? "none" : h.driver}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {h.subnets?.join(", ") || "—"}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{h.containers}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {editing && (
        <NetworkDialog
          nodeId={nodeId}
          network={editing === "new" ? undefined : editing}
          onClose={() => setEditing(undefined)}
        />
      )}
    </div>
  );
}

function NetworkRow({
  orgId,
  name,
  traits = [],
  about,
  servers,
  addresses,
  actions,
}: {
  orgId: string;
  name: string;
  traits?: string[];
  about: ReactNode;
  servers: Server[];
  // Servers' fixed addresses on the network, by server ID.
  addresses?: Record<string, string>;
  actions?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-3">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2 py-0.5">
          <Network className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate font-medium">{name}</span>
          {traits.map((t) => (
            <Badge key={t} variant="secondary">
              {t}
            </Badge>
          ))}
        </div>
        {actions && <div className="-my-0.5 flex shrink-0 gap-1">{actions}</div>}
      </div>
      <p className="text-xs text-muted-foreground">{about}</p>
      {servers.length === 0 ? (
        <p className="text-sm text-muted-foreground">No servers.</p>
      ) : (
        <ul className="flex flex-wrap gap-1.5">
          {servers.map((s) => (
            <li key={s.id}>
              <Link
                to="/orgs/$orgId/servers/$serverId/network"
                params={{ orgId, serverId: shortId(s.id) }}
                className="inline-flex items-baseline gap-1.5 rounded-md border px-2 py-0.5 text-sm transition-colors hover:bg-muted"
              >
                {s.name}
                {addresses?.[s.id] && (
                  <span className="font-mono text-xs text-muted-foreground">{addresses[s.id]}</span>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function DeleteButton({ nodeId, network }: { nodeId: string; network: RaptorNetwork }) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const inUse = network.servers.length > 0;

  async function remove() {
    const ok = await ask.confirm({
      title: `Delete ${network.name}?`,
      description: "The network is removed from the machine. No server is on it.",
      confirm: "Delete network",
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "node.network.delete",
        paramsJson: JSON.stringify({ id: network.id }),
      });
      await client.invalidateQueries({ queryKey: networksKey(nodeId) });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Delete ${network.name}`}
        title={inUse ? "Move its servers to another network first" : undefined}
        disabled={busy || inUse}
        onClick={remove}
      >
        {busy ? <Loader2 className="animate-spin" /> : <Trash2 />}
      </Button>
      {error && <p className="basis-full text-sm text-destructive">{error}</p>}
    </>
  );
}

// NetworkDialog makes a network or changes one. A name is all a new one
// needs; Advanced has the rest.
function NetworkDialog({
  nodeId,
  network,
  onClose,
}: {
  nodeId: string;
  network?: RaptorNetwork;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState(network?.name ?? "");
  const [subnet, setSubnet] = useState(network?.subnet ?? "");
  const [internet, setInternet] = useState(network?.internet ?? true);
  const [hostAccess, setHostAccess] = useState(network?.host_access ?? true);
  const [ipv6, setIPv6] = useState(network?.ipv6 ?? false);
  const [mtu, setMTU] = useState(network?.mtu ? String(network.mtu) : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const input: NetworkInput = {
    id: network?.id,
    name: name.trim(),
    subnet: subnet.trim() || undefined,
    internet,
    ipv6,
    mtu: mtu ? Number(mtu) : undefined,
    host_access: hostAccess,
  };
  const restarts = !!network && recreates(network, input);
  // Settings changed from the defaults open Advanced from the start.
  const advanced = !!network && networkTraits(network).length > 0;

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "node.network.save",
        paramsJson: JSON.stringify(input),
      });
      await client.invalidateQueries({ queryKey: networksKey(nodeId) });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={save} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{network ? `Edit ${network.name}` : "New network"}</DialogTitle>
            <DialogDescription>
              {network
                ? "Its servers keep their place on it."
                : "Servers you put on it reach each other, and nothing on other networks."}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="network-name">Name</Label>
            <Input
              id="network-name"
              autoFocus
              required
              maxLength={40}
              placeholder="Community A"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <details className="rounded-lg border p-3" open={advanced}>
            <summary className="cursor-pointer text-sm font-medium">Advanced</summary>
            <div className="mt-3 flex flex-col gap-4">
              <Toggle
                id="network-internet"
                label="Internet access"
                about="Off keeps its servers from connecting out, for backends that only talk to their proxy. Players still reach their ports."
                checked={internet}
                onChange={setInternet}
              />
              <Toggle
                id="network-host"
                label="Reach the machine"
                about="Off keeps its servers from connecting to services on this machine itself."
                checked={hostAccess}
                onChange={setHostAccess}
              />
              <Toggle
                id="network-ipv6"
                label="IPv6"
                about="Gives the network an IPv6 range too."
                checked={ipv6}
                onChange={setIPv6}
              />
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="network-subnet">Subnet</Label>
                  <Input
                    id="network-subnet"
                    className="font-mono"
                    placeholder="Picked for you"
                    spellCheck={false}
                    value={subnet}
                    onChange={(e) => setSubnet(e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">
                    A private range like 10.210.0.0/24.
                  </p>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="network-mtu">MTU</Label>
                  <Input
                    id="network-mtu"
                    type="number"
                    min={1280}
                    max={9000}
                    placeholder="Default"
                    value={mtu}
                    onChange={(e) => setMTU(e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">Lower it under a VPN.</p>
                </div>
              </div>
            </div>
          </details>
          {restarts && (
            <p className="text-sm text-muted-foreground">
              This recreates the network, so its servers have to be stopped first.
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy && <Loader2 className="animate-spin" />}
              {network ? "Save" : "Create network"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Toggle({
  id,
  label,
  about,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  about: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="flex flex-col gap-0.5">
        <Label htmlFor={id}>{label}</Label>
        <p className="text-xs text-muted-foreground">{about}</p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}
