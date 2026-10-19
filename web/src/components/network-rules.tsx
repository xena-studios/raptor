import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Loader2, Plus, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
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
import type { Server } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { type NetworkRule, networksKey, type RaptorNetwork } from "@/lib/networks";
import { commandClient } from "@/lib/transport";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// NetworkRules lists the rules between a node's networks and adds or
// removes them (node.network.rules replaces the whole list).
export function NetworkRules({
  nodeId,
  networks,
  rules,
  servers,
}: {
  nodeId: string;
  networks: RaptorNetwork[];
  rules: NetworkRule[];
  servers: Map<string, Server>;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(-1);
  const [error, setError] = useState("");
  const names = new Map(networks.map((n) => [n.id, n.name]));
  const serverName = (id?: string) => (id ? (servers.get(id)?.name ?? "a deleted server") : "");
  // A rule naming a server lets nothing through until the server has its
  // fixed address on that network, at its first start there.
  const waiting = (r: NetworkRule) =>
    [
      [r.from_server, r.from],
      [r.to_server, r.to],
    ]
      .filter(([sv, n]) => sv && !networks.find((x) => x.id === n)?.addresses?.[sv])
      .map(([sv]) => serverName(sv));

  async function save(next: NetworkRule[]) {
    await commandClient.execute({
      nodeId,
      action: "node.network.rules",
      paramsJson: JSON.stringify({ rules: next }),
    });
    await client.invalidateQueries({ queryKey: networksKey(nodeId) });
  }

  async function remove(i: number) {
    const r = rules[i];
    if (!r) return;
    const ok = await ask.confirm({
      title: "Remove this rule?",
      description: `${side(r.from, r.from_server)} can't make new connections to ${side(r.to, r.to_server)}. Ones already open carry on until they close.`,
      confirm: "Remove rule",
      destructive: true,
    });
    if (!ok) return;
    setBusy(i);
    setError("");
    try {
      await save(rules.filter((_, j) => j !== i));
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(-1);
    }
  }

  function side(network: string, server?: string) {
    const n = names.get(network) ?? "A deleted network";
    return server ? `${serverName(server)} on ${n}` : n;
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Rules between networks</CardTitle>
        <CardDescription>
          Let one network, or one server on it, reach another on the ports you pick. Each rule works
          one way; replies get back.
        </CardDescription>
        <CardAction>
          <Button
            size="sm"
            variant="outline"
            disabled={networks.length < 2}
            onClick={() => setAdding(true)}
          >
            <Plus /> Add rule
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {networks.length < 2 ? (
          <p className="text-sm text-muted-foreground">Rules need at least two networks.</p>
        ) : rules.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No rules: no network reaches another. A proxy can also join several networks from its
            Network tab instead.
          </p>
        ) : (
          <ul className="flex flex-col gap-2">
            {rules.map((r, i) => (
              <li
                key={`${r.from}-${r.from_server}-${r.to}-${r.to_server}-${r.protocol}-${r.ports}`}
                className="flex items-start justify-between gap-3 rounded-lg border p-3"
              >
                <div className="flex min-w-0 flex-col gap-1">
                  <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-medium">
                    <span>{side(r.from, r.from_server)}</span>
                    <ArrowRight className="size-4 text-muted-foreground" aria-label="reaches" />
                    <span>{side(r.to, r.to_server)}</span>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {portsLabel(r)}
                    {r.note ? ` · ${r.note}` : ""}
                  </p>
                  {waiting(r).length > 0 && (
                    <p className="text-xs text-muted-foreground">
                      Applies once {waiting(r).join(" and ")} has started on this network.
                    </p>
                  )}
                </div>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Remove rule"
                  disabled={busy !== -1}
                  onClick={() => remove(i)}
                >
                  {busy === i ? <Loader2 className="animate-spin" /> : <X />}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      {adding && (
        <RuleDialog
          networks={networks}
          servers={servers}
          onSave={(r) => save([...rules, r])}
          onClose={() => setAdding(false)}
        />
      )}
    </Card>
  );
}

// portsLabel is a rule's protocol and ports in words.
export function portsLabel(r: NetworkRule): string {
  const proto = r.protocol === "tcp" ? "TCP" : r.protocol === "udp" ? "UDP" : "TCP and UDP";
  if (!r.ports)
    return r.protocol === "tcp" || r.protocol === "udp" ? `All ${proto} ports` : "All ports";
  return `${proto} ${r.ports.replaceAll(",", ", ")}`;
}

function RuleDialog({
  networks,
  servers,
  onSave,
  onClose,
}: {
  networks: RaptorNetwork[];
  servers: Map<string, Server>;
  onSave: (r: NetworkRule) => Promise<void>;
  onClose: () => void;
}) {
  const [from, setFrom] = useState(networks[0]?.id ?? "");
  const [to, setTo] = useState(networks[1]?.id ?? "");
  const [fromServer, setFromServer] = useState("");
  const [toServer, setToServer] = useState("");
  const [protocol, setProtocol] = useState<"tcp" | "udp" | "both">("tcp");
  const [ports, setPorts] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const on = (id: string) =>
    (networks.find((n) => n.id === id)?.servers ?? [])
      .map((s) => servers.get(s))
      .filter((s): s is Server => !!s);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave({
        from,
        from_server: fromServer || undefined,
        to,
        to_server: toServer || undefined,
        protocol,
        ports: ports.trim() || undefined,
        note: note.trim() || undefined,
      });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const sideFields = (
    label: string,
    id: string,
    network: string,
    setNetwork: (v: string) => void,
    server: string,
    setServer: (v: string) => void,
  ) => (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
        {label}
      </legend>
      <div className="grid gap-2 sm:grid-cols-2">
        <select
          aria-label={`${label}: network`}
          className={select}
          value={network}
          onChange={(e) => {
            setNetwork(e.target.value);
            setServer("");
          }}
        >
          {networks.map((n) => (
            <option key={n.id} value={n.id}>
              {n.name}
            </option>
          ))}
        </select>
        <select
          id={id}
          aria-label={`${label}: server`}
          className={select}
          value={server}
          onChange={(e) => setServer(e.target.value)}
        >
          <option value="">Any server on it</option>
          {on(network).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      </div>
    </fieldset>
  );

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Add a rule</DialogTitle>
            <DialogDescription>
              Connections from the first reach the second, and their replies get back.
            </DialogDescription>
          </DialogHeader>
          {sideFields("From", "rule-from-server", from, setFrom, fromServer, setFromServer)}
          {sideFields("To", "rule-to-server", to, setTo, toServer, setToServer)}
          <div className="grid gap-4 sm:grid-cols-[8rem_1fr]">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-protocol">Protocol</Label>
              <select
                id="rule-protocol"
                className={select}
                value={protocol}
                onChange={(e) => setProtocol(e.target.value as "tcp" | "udp" | "both")}
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="both">Both</option>
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rule-ports">Ports</Label>
              <Input
                id="rule-ports"
                placeholder="All ports"
                spellCheck={false}
                value={ports}
                onChange={(e) => setPorts(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">Like 25565, or 27015-27020.</p>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rule-note">Note</Label>
            <Input
              id="rule-note"
              maxLength={100}
              placeholder="Optional"
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </div>
          {from === to && (
            <p className="text-sm text-muted-foreground">
              Servers on one network already reach each other: pick two.
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !from || !to || from === to}>
              {busy && <Loader2 className="animate-spin" />} Add rule
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
