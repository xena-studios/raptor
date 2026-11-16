import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Loader2, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { type Content, useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { nodeHostname } from "@/lib/addresses";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { withProperty } from "@/lib/minecraft-text";
import { can, useOrgServers } from "@/lib/org-data";
import {
  type Backend,
  paperForVelocity,
  readBackends,
  readBungeeBackends,
  spigotForBungee,
  validBackendName,
  writeBackends,
  writeBungeeBackends,
} from "@/lib/proxy-backends";
import { base64 } from "@/lib/servers";
import { commandClient } from "@/lib/transport";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// Paper and the forks that take Velocity's modern forwarding.
const paperFamily = /^Minecraft: (Paper|Purpur|Folia|Leaf|DivineMC|CanvasMC|Pufferfish)\b/;
// BungeeCord's forwarding is Spigot's, so Spigot itself too.
const spigotFamily = /^Minecraft: (Spigot|Paper|Purpur|Folia|Leaf|DivineMC|CanvasMC|Pufferfish)\b/;

export type ProxyKind = "velocity" | "bungee";

const kinds = {
  velocity: {
    file: "velocity.toml",
    name: "Velocity",
    read: readBackends,
    write: writeBackends,
    family: paperFamily,
    servers: "Paper",
  },
  bungee: {
    file: "config.yml",
    name: "the proxy",
    read: readBungeeBackends,
    write: writeBungeeBackends,
    family: spigotFamily,
    servers: "Spigot or Paper",
  },
} as const;

async function readFile(
  nodeId: string,
  serverId: string,
  path: string,
): Promise<string | undefined> {
  try {
    const res = await commandClient.execute({
      nodeId,
      action: "files.read",
      serverId,
      paramsJson: JSON.stringify({ path }),
    });
    const f = JSON.parse(res.resultJson || "{}") as { data?: string };
    return new TextDecoder().decode(fromBase64(f.data ?? ""));
  } catch {
    return undefined;
  }
}

async function writeFile(nodeId: string, serverId: string, path: string, text: string) {
  await commandClient.execute({
    nodeId,
    action: "files.write",
    serverId,
    paramsJson: JSON.stringify({ path, data: base64(new TextEncoder().encode(text)) }),
  });
}

const slug = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32) || "server";

// ProxyBackends sets a proxy's backends from the org's servers (#427).
// Velocity: its [servers] list and try order, modern forwarding, and each
// Paper backend set to take players only from this proxy (its forwarding
// secret, so nobody can skip the proxy and join as someone else).
// BungeeCord and Waterfall: config.yml's servers and priorities,
// ip_forward, and each backend's bungeecord setting; their forwarding has
// no secret, so the card says to keep backends unreachable from outside.
export function ProxyBackends({
  orgId,
  nodeId,
  serverId,
  canWrite,
  kind = "velocity",
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  canWrite: boolean;
  kind?: ProxyKind;
}) {
  const k = kinds[kind];
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const toml = useTanQuery({
    queryKey: api.contentKey(k.file),
    queryFn: async () => {
      const f = await api.run<Content>("files.read", { path: k.file });
      return new TextDecoder().decode(fromBase64(f?.data ?? ""));
    },
    retry: false,
  });
  const { servers } = useOrgServers(orgId, 30_000);
  const candidates = useMemo(
    () =>
      servers.filter(
        (s) =>
          s.id !== serverId &&
          k.family.test(s.eggName) &&
          can(s, "files.write") &&
          s.ports.length > 0,
      ),
    [servers, serverId, k.family],
  );
  const current = useMemo(() => k.read(toml.data ?? ""), [toml.data, k]);
  const [list, setList] = useState<Backend[] | undefined>(undefined);
  const backends =
    list ??
    current.servers.slice().sort((a, b) => {
      const ia = current.try.indexOf(a.name);
      const ib = current.try.indexOf(b.name);
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
    });
  // The org servers being added, by backend name, to set up on save.
  const [added, setAdded] = useState<Record<string, { nodeId: string; serverId: string }>>({});
  const [pick, setPick] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  if (toml.isPending) return null;
  if (toml.isError || !toml.data) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Backends</CardTitle>
          <CardDescription>
            Start {k.name === "Velocity" ? "Velocity" : "the proxy"} once so it makes {k.file}; then
            pick its backends here.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  const picked = candidates.find((s) => s.id === pick);
  const nameOk = validBackendName(name) && !backends.some((b) => b.name === name);

  function add() {
    if (!picked || !nameOk) return;
    const address = `${nodeHostname(picked.node)}:${picked.ports[0]}`;
    setList([...backends, { name, address }]);
    setAdded({ ...added, [name]: { nodeId: picked.node.id, serverId: picked.id } });
    setPick("");
    setName("");
  }

  function move(i: number, d: number) {
    const next = backends.slice();
    const [b] = next.splice(i, 1);
    if (b) next.splice(i + d, 0, b);
    setList(next);
  }

  async function save() {
    const adding = Object.keys(added).filter((n) => backends.some((x) => x.name === n));
    if (kind === "bungee" && adding.length > 0) {
      const ok = await confirm({
        title: "Keep these servers behind the proxy",
        description:
          "BungeeCord's forwarding has no secret: anyone who can reach a backend's port directly can join it as any player, admins included. Only add servers whose ports aren't open to the internet (a private network, or a firewall that only lets the proxy in). Velocity's forwarding doesn't have this problem.",
        confirm: "I understand, save",
        destructive: true,
      });
      if (!ok) return;
    }
    setBusy(true);
    setError("");
    setNote("");
    try {
      const secret =
        kind === "velocity" ? (await readFile(nodeId, serverId, "forwarding.secret"))?.trim() : "";
      if (kind === "velocity" && !secret)
        throw new Error("Velocity has no forwarding.secret yet: start it once, then save again.");
      const pending: string[] = [];
      for (const bname of adding) {
        const b = added[bname];
        if (!b) continue;
        const path = kind === "velocity" ? "config/paper-global.yml" : "spigot.yml";
        const yml = await readFile(b.nodeId, b.serverId, path);
        const props = await readFile(b.nodeId, b.serverId, "server.properties");
        const changed = yml
          ? kind === "velocity"
            ? paperForVelocity(yml, secret ?? "")
            : spigotForBungee(yml)
          : undefined;
        if (!changed || props === undefined) {
          pending.push(bname);
          continue;
        }
        await writeFile(b.nodeId, b.serverId, path, changed);
        await writeFile(
          b.nodeId,
          b.serverId,
          "server.properties",
          withProperty(props, "online-mode", "false"),
        );
      }
      await writeFile(
        nodeId,
        serverId,
        k.file,
        k.write(
          toml.data ?? "",
          backends,
          backends.map((b) => b.name),
        ),
      );
      await client.invalidateQueries({ queryKey: api.contentKey(k.file) });
      setList(undefined);
      setAdded({});
      setNote(
        `Saved. Restart ${k.name} and the servers you added.${pending.length ? ` ${pending.join(", ")} ${pending.length === 1 ? "hasn't" : "haven't"} made ${kind === "velocity" ? "Paper's" : "Spigot's"} settings yet: start ${pending.length === 1 ? "it" : "them"} once, then save here again to finish.` : ""}`,
      );
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const dirty = list !== undefined;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Backends</CardTitle>
        <CardDescription>
          {kind === "velocity"
            ? "The servers behind this proxy, tried in this order when players join. Adding one of your Paper servers sets it to take players only through this proxy (Velocity's modern forwarding, with its secret), so nobody can join it directly as someone else."
            : "The servers behind this proxy, tried in this order when players join. Adding one of your Spigot or Paper servers turns on BungeeCord forwarding on it and on the proxy, and turns its own login check off."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {backends.length === 0 ? (
          <p className="text-muted-foreground">No backends yet.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {backends.map((b, i) => (
              <li key={b.name} className="flex items-center gap-2 px-3 py-1.5">
                <span className="w-6 text-xs text-muted-foreground">{i + 1}</span>
                <span className="min-w-0 flex-1 truncate font-medium">{b.name}</span>
                <code className="hidden truncate text-xs text-muted-foreground sm:inline">
                  {b.address}
                </code>
                {canWrite && (
                  <>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Try ${b.name} earlier`}
                      disabled={i === 0}
                      onClick={() => move(i, -1)}
                    >
                      <ArrowUp />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Try ${b.name} later`}
                      disabled={i === backends.length - 1}
                      onClick={() => move(i, 1)}
                    >
                      <ArrowDown />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Remove ${b.name}`}
                      className="text-muted-foreground hover:text-destructive"
                      onClick={() => setList(backends.filter((x) => x.name !== b.name))}
                    >
                      <Trash2 />
                    </Button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
        {canWrite && (
          <div className="flex flex-wrap items-center gap-2">
            <select
              aria-label="One of your servers"
              className={select}
              value={pick}
              onChange={(e) => {
                setPick(e.target.value);
                const s = candidates.find((c) => c.id === e.target.value);
                if (s && !name) setName(slug(s.name));
              }}
            >
              <option value="">Add one of your {k.servers} servers…</option>
              {candidates.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} ({s.node.name})
                </option>
              ))}
            </select>
            <Input
              aria-label="Its name in the proxy"
              placeholder="Name"
              className="w-36"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <Button size="sm" variant="outline" disabled={!picked || !nameOk} onClick={add}>
              Add
            </Button>
          </div>
        )}
        {canWrite && dirty && (
          <Button className="self-start" disabled={busy} onClick={() => void save()}>
            {busy && <Loader2 className="animate-spin" />} Save
          </Button>
        )}
        {kind === "bungee" && (
          <p className="rounded-lg border border-destructive/40 px-3 py-2 text-xs">
            BungeeCord forwarding has no secret: a backend whose port is open to the internet can be
            joined directly as any player. Keep backends reachable only by the proxy, or use
            Velocity, whose forwarding is signed.
          </p>
        )}
        <p className="text-xs text-muted-foreground">
          Removing a backend only takes it out of {k.name}; it keeps its forwarding settings until
          you change them.
        </p>
        {note && <p className="text-muted-foreground">{note}</p>}
        {error && <p className="text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
