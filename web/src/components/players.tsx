import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Ban, Crown, Loader2, LogOut, ShieldCheck, UserMinus, UserPlus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { type Content, useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import {
  type Ban as BanEntry,
  commands,
  type IpBan,
  listFiles,
  mcDate,
  type Op,
  type Player,
  parseList,
  reasonText,
  type Seen,
  validIp,
  validName,
  withEntry,
  without,
} from "@/lib/minecraft-players";
import { propertyValue } from "@/lib/minecraft-text";
import { base64 } from "@/lib/servers";
import { addonClient, commandClient } from "@/lib/transport";

type ListKey = keyof typeof listFiles;

// players.lists' answer: the list files, and the two settings they hang on.
type PlayerLists = {
  whitelist?: Player[];
  ops?: Op[];
  bans?: BanEntry[];
  ip_bans?: IpBan[];
  seen?: Seen[];
  properties?: Record<string, string>;
};

type ManageParams = {
  op: string;
  name?: string;
  uuid?: string;
  ip?: string;
  reason?: string;
};

// Players manages a Minecraft server's whitelist, operators, and bans
// (#422). Running, it tells the game (console commands, as typing them
// would); stopped, it edits the files the game reads at its next start,
// looking players' UUIDs up (offline-mode servers: the offline UUID).
export function Players({
  orgId,
  nodeId,
  serverId,
  running,
  canPlayers,
  canCommand,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  running: boolean;
  // The players permission: Wings' players.* actions, running or not.
  canPlayers: boolean;
  // Without it: console.write (running), files.write (stopped).
  canCommand: boolean;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const byFiles = !canPlayers;
  const files = {
    whitelist: useTextFile(api, listFiles.whitelist, byFiles),
    ops: useTextFile(api, listFiles.ops, byFiles),
    bans: useTextFile(api, listFiles.bans, byFiles),
    ipBans: useTextFile(api, listFiles.ipBans, byFiles),
    seen: useTextFile(api, listFiles.seen, byFiles),
    props: useTextFile(api, "server.properties", byFiles),
  };
  const playersKey = ["players.lists", nodeId, serverId];
  const fromWings = useTanQuery({
    enabled: canPlayers,
    queryKey: playersKey,
    queryFn: () => api.run<PlayerLists>("players.lists", {}),
    refetchOnWindowFocus: false,
  });
  const w = fromWings.data;
  // Who's online, from the game's ping (players.online): refreshed while
  // the tab is open.
  const online = useTanQuery({
    enabled: canPlayers && running,
    queryKey: ["players.online", nodeId, serverId],
    queryFn: () =>
      api.run<{ running: boolean; online: number; max: number; names: string[] | null }>(
        "players.online",
        {},
      ),
    refetchInterval: 15_000,
    retry: false,
  });
  const lists = w
    ? {
        whitelist: w.whitelist ?? [],
        ops: w.ops ?? [],
        bans: w.bans ?? [],
        ipBans: w.ip_bans ?? [],
        seen: w.seen ?? [],
      }
    : {
        whitelist: parseList<Player>(files.whitelist.data),
        ops: parseList<Op>(files.ops.data),
        bans: parseList<BanEntry>(files.bans.data),
        ipBans: parseList<IpBan>(files.ipBans.data),
        seen: parseList<Seen>(files.seen.data),
      };
  const prop = (k: string) => (w ? w.properties?.[k] : propertyValue(files.props.data ?? "", k));
  const offline = prop("online-mode") === "false";
  const whitelistOn = prop("white-list") === "true";
  const can = canPlayers || (running ? canCommand : canWrite);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  const refresh = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: playersKey }),
      client.invalidateQueries({ queryKey: ["players.online", nodeId, serverId] }),
      ...Object.values(listFiles).map((p) =>
        client.invalidateQueries({ queryKey: api.contentKey(p) }),
      ),
    ]);

  async function run(
    label: string,
    cmd: string,
    edit: () => Promise<void>,
    manage?: () => Promise<ManageParams>,
  ) {
    setBusy(label);
    setError("");
    setNote("");
    try {
      if (canPlayers && manage) {
        await api.run("players.manage", await manage());
        if (running) await new Promise((r) => setTimeout(r, 1500));
      } else if (running) {
        await commandClient.execute({
          nodeId,
          action: "server.command",
          serverId,
          paramsJson: JSON.stringify({ command: cmd }),
        });
        // The game writes its file right away; read it again shortly.
        await new Promise((r) => setTimeout(r, 1500));
      } else {
        await edit();
      }
      await refresh();
      setNote(running ? "Done." : "Saved. The server uses it when it starts.");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  async function lookup(name: string): Promise<Player> {
    const p = await addonClient.lookupMinecraftPlayer({ orgId, name, offline });
    return { name: p.name, uuid: p.uuid };
  }

  // who is a player for players.manage: a stopped server's files need the
  // UUID, which the Panel looks up.
  async function who(name: string): Promise<{ name: string; uuid?: string }> {
    if (running) return { name };
    return lookup(name);
  }

  async function write(key: ListKey, text: string) {
    await api.run("files.write", {
      path: listFiles[key],
      data: base64(new TextEncoder().encode(text)),
    });
  }

  const actions = {
    whitelist: (name: string) =>
      run(
        `whitelist:${name}`,
        commands.whitelistAdd(name),
        async () => write("whitelist", withEntry(lists.whitelist, await lookup(name), "uuid")),
        async () => ({ op: "whitelist.add", ...(await who(name)) }),
      ),
    unwhitelist: (name: string) =>
      run(
        `unwhitelist:${name}`,
        commands.whitelistRemove(name),
        () => write("whitelist", without(lists.whitelist, "name", name)),
        async () => ({ op: "whitelist.remove", name }),
      ),
    op: (name: string) =>
      run(
        `op:${name}`,
        commands.op(name),
        async () =>
          write(
            "ops",
            withEntry(
              lists.ops,
              { ...(await lookup(name)), level: 4, bypassesPlayerLimit: false },
              "uuid",
            ),
          ),
        async () => ({ op: "op", ...(await who(name)) }),
      ),
    deop: (name: string) =>
      run(
        `deop:${name}`,
        commands.deop(name),
        () => write("ops", without(lists.ops, "name", name)),
        async () => ({ op: "deop", name }),
      ),
    ban: (name: string, reason: string) =>
      run(
        `ban:${name}`,
        commands.ban(name, reason),
        async () =>
          write(
            "bans",
            withEntry(
              lists.bans,
              {
                ...(await lookup(name)),
                created: mcDate(new Date()),
                source: "Raptor",
                expires: "forever",
                reason: reasonText(reason) || "Banned by an operator.",
              },
              "uuid",
            ),
          ),
        async () => ({ op: "ban", reason, ...(await who(name)) }),
      ),
    pardon: (name: string) =>
      run(
        `pardon:${name}`,
        commands.pardon(name),
        () => write("bans", without(lists.bans, "name", name)),
        async () => ({ op: "pardon", name }),
      ),
    banIp: (ip: string, reason: string) =>
      run(
        `banip:${ip}`,
        commands.banIp(ip, reason),
        () =>
          write(
            "ipBans",
            withEntry(
              lists.ipBans,
              {
                ip,
                created: mcDate(new Date()),
                source: "Raptor",
                expires: "forever",
                reason: reasonText(reason) || "Banned by an operator.",
              },
              "ip",
            ),
          ),
        async () => ({ op: "ban-ip", ip, reason }),
      ),
    pardonIp: (ip: string) =>
      run(
        `pardonip:${ip}`,
        commands.pardonIp(ip),
        () => write("ipBans", without(lists.ipBans, "ip", ip)),
        async () => ({ op: "pardon-ip", ip }),
      ),
    kick: (name: string) =>
      run(
        `kick:${name}`,
        commands.kick(name),
        async () => {},
        async () => ({ op: "kick", name }),
      ),
  };

  const loading = canPlayers ? fromWings.isPending : Object.values(files).some((f) => f.isPending);
  if (fromWings.error)
    return <p className="text-sm text-destructive">{message(fromWings.error)}</p>;
  if (loading) return <p className="text-sm text-muted-foreground">Loading players…</p>;
  const disabled = !can || !!busy;

  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-muted-foreground">
        {running
          ? "The server is running: changes are made by telling the game, as typing the commands would."
          : "The server is stopped: changes are saved to its files, and it uses them when it starts."}
        {offline &&
          " It's in offline mode (online-mode=false): players aren't checked with Mojang, so anyone can join as any name unless a proxy checks them."}
        {!can &&
          (running
            ? " Changing them needs the Send console commands permission."
            : " Changing them needs the Change files permission.")}
      </p>
      {canPlayers && running && (
        <Section
          title={
            online.data?.running
              ? `Online now: ${online.data.online} of ${online.data.max}`
              : "Online now"
          }
          description={
            online.error
              ? `Couldn't ask the server: ${message(online.error)}`
              : online.data?.running && (online.data.names?.length ?? 0) < online.data.online
                ? "Minecraft only lists some names (up to 12, and servers can hide them); type list in the console for everyone."
                : "Players on the server right now."
          }
          empty={online.isPending ? "Asking the server…" : "Nobody's online."}
          rows={(online.data?.names ?? []).map((name) => {
            const op = lists.ops.some((o) => o.name.toLowerCase() === name.toLowerCase());
            return {
              key: name,
              label: name,
              detail: op ? "operator" : "",
              actions: (
                <>
                  {!op && (
                    <RowButton
                      icon={<Crown />}
                      label={`Make ${name} an operator`}
                      busy={busy === `op:${name}`}
                      disabled={disabled}
                      onClick={() => actions.op(name)}
                    />
                  )}
                  <RowButton
                    icon={<LogOut />}
                    label={`Kick ${name}`}
                    busy={busy === `kick:${name}`}
                    disabled={disabled}
                    onClick={() => actions.kick(name)}
                  />
                  <RowButton
                    icon={<Ban />}
                    label={`Ban ${name}`}
                    busy={busy === `ban:${name}`}
                    disabled={disabled}
                    onClick={() => actions.ban(name, "")}
                  />
                </>
              ),
            };
          })}
        />
      )}
      {note && <p className="text-sm text-muted-foreground">{note}</p>}
      {error && <p className="text-sm text-destructive">{error}</p>}

      <Section
        title="Whitelist"
        description={
          whitelistOn
            ? "Only these players can join."
            : "The whitelist is off: anyone can join. Turn it on in Game → Game settings."
        }
        add={{
          label: "Add",
          placeholder: "Player name",
          onAdd: (n) => actions.whitelist(n),
          disabled,
        }}
        empty="Nobody yet."
        rows={lists.whitelist.map((p) => ({
          key: p.uuid || p.name,
          label: p.name,
          actions: (
            <RowButton
              icon={<UserMinus />}
              label={`Take ${p.name} off the whitelist`}
              busy={busy === `unwhitelist:${p.name}`}
              disabled={disabled}
              onClick={() => actions.unwhitelist(p.name)}
            />
          ),
        }))}
      />

      <Section
        title="Operators"
        description="Operators can run every command (their level is in ops.json)."
        add={{
          label: "Make operator",
          placeholder: "Player name",
          onAdd: (n) => actions.op(n),
          disabled,
        }}
        empty="No operators."
        rows={lists.ops.map((p) => ({
          key: p.uuid || p.name,
          label: p.name,
          detail: `Level ${p.level}`,
          actions: (
            <RowButton
              icon={<UserMinus />}
              label={`Take operator from ${p.name}`}
              busy={busy === `deop:${p.name}`}
              disabled={disabled}
              onClick={() => actions.deop(p.name)}
            />
          ),
        }))}
      />

      <Section
        title="Banned players"
        description="They can't join until they're unbanned."
        add={{
          label: "Ban",
          placeholder: "Player name",
          reason: true,
          onAdd: (n, r) => actions.ban(n, r),
          disabled,
        }}
        empty="Nobody's banned."
        rows={lists.bans.map((b) => ({
          key: b.uuid || b.name,
          label: b.name,
          detail: [
            b.reason,
            b.created && `since ${b.created.slice(0, 10)}`,
            b.expires !== "forever" && `until ${b.expires.slice(0, 10)}`,
          ]
            .filter(Boolean)
            .join(" · "),
          actions: (
            <RowButton
              icon={<ShieldCheck />}
              label={`Unban ${b.name}`}
              busy={busy === `pardon:${b.name}`}
              disabled={disabled}
              onClick={() => actions.pardon(b.name)}
            />
          ),
        }))}
      />

      <Section
        title="Banned addresses"
        description="Nobody can join from these IP addresses."
        add={{
          label: "Ban address",
          placeholder: "IP address",
          reason: true,
          valid: validIp,
          invalidText: "That isn't an IP address.",
          onAdd: (ip, r) => actions.banIp(ip, r),
          disabled,
        }}
        empty="No addresses are banned."
        rows={lists.ipBans.map((b) => ({
          key: b.ip,
          label: b.ip,
          detail: b.reason,
          actions: (
            <RowButton
              icon={<ShieldCheck />}
              label={`Unban ${b.ip}`}
              busy={busy === `pardonip:${b.ip}`}
              disabled={disabled}
              onClick={() => actions.pardonIp(b.ip)}
            />
          ),
        }))}
      />

      <Section
        title="Seen before"
        description="Players who've joined this server (usercache.json)."
        empty="Nobody has joined yet."
        rows={lists.seen.slice(0, 200).map((p) => {
          const listed = lists.whitelist.some((w) => w.name.toLowerCase() === p.name.toLowerCase());
          const op = lists.ops.some((o) => o.name.toLowerCase() === p.name.toLowerCase());
          const banned = lists.bans.some((b) => b.name.toLowerCase() === p.name.toLowerCase());
          return {
            key: p.uuid || p.name,
            label: p.name,
            detail: [listed && "whitelisted", op && "operator", banned && "banned"]
              .filter(Boolean)
              .join(" · "),
            actions: (
              <>
                {!listed && (
                  <RowButton
                    icon={<UserPlus />}
                    label={`Whitelist ${p.name}`}
                    busy={busy === `whitelist:${p.name}`}
                    disabled={disabled}
                    onClick={() => actions.whitelist(p.name)}
                  />
                )}
                {!op && (
                  <RowButton
                    icon={<Crown />}
                    label={`Make ${p.name} an operator`}
                    busy={busy === `op:${p.name}`}
                    disabled={disabled}
                    onClick={() => actions.op(p.name)}
                  />
                )}
                {running && (
                  <RowButton
                    icon={<LogOut />}
                    label={`Kick ${p.name}`}
                    busy={busy === `kick:${p.name}`}
                    disabled={disabled}
                    onClick={() => actions.kick(p.name)}
                  />
                )}
                {!banned && (
                  <RowButton
                    icon={<Ban />}
                    label={`Ban ${p.name}`}
                    busy={busy === `ban:${p.name}`}
                    disabled={disabled}
                    onClick={() => actions.ban(p.name, "")}
                  />
                )}
              </>
            ),
          };
        })}
      />
    </div>
  );
}

// useTextFile is a server file's text; "" when it isn't there yet.
function useTextFile(api: ReturnType<typeof useFilesApi>, path: string, enabled: boolean) {
  return useTanQuery({
    enabled,
    queryKey: api.contentKey(path),
    queryFn: async () => {
      try {
        const f = await api.run<Content>("files.read", { path });
        return new TextDecoder().decode(fromBase64(f?.data ?? ""));
      } catch {
        return "";
      }
    },
    refetchOnWindowFocus: false,
  });
}

function RowButton({
  icon,
  label,
  busy,
  disabled,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  busy: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      {busy ? <Loader2 className="animate-spin" /> : icon}
    </Button>
  );
}

function Section({
  title,
  description,
  add,
  empty,
  rows,
}: {
  title: string;
  description: string;
  add?: {
    label: string;
    placeholder: string;
    reason?: boolean;
    valid?: (s: string) => boolean;
    invalidText?: string;
    onAdd: (value: string, reason: string) => void;
    disabled: boolean;
  };
  empty: string;
  rows: { key: string; label: string; detail?: string; actions: ReactNode }[];
}) {
  const [value, setValue] = useState("");
  const [reason, setReason] = useState("");
  const valid = add?.valid ?? validName;
  const ok = valid(value.trim());
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!add || !ok) return;
    add.onAdd(value.trim(), reason);
    setValue("");
    setReason("");
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {add && (
          <form onSubmit={submit} className="flex flex-wrap items-start gap-2">
            <div className="flex flex-col gap-1">
              <Input
                aria-label={add.placeholder}
                placeholder={add.placeholder}
                className="w-48"
                spellCheck={false}
                autoComplete="off"
                value={value}
                onChange={(e) => setValue(e.target.value)}
              />
              {value.trim() && !ok && (
                <span className="text-xs text-destructive">
                  {add.invalidText ?? "1 to 16 letters, digits, and underscores."}
                </span>
              )}
            </div>
            {add.reason && (
              <Input
                aria-label="Reason"
                placeholder="Reason (optional)"
                className="w-56"
                maxLength={100}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            )}
            <Button type="submit" variant="outline" disabled={add.disabled || !ok}>
              {add.label}
            </Button>
          </form>
        )}
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{empty}</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {rows.map((r) => (
              <li key={r.key} className="flex items-center gap-3 px-3 py-1.5 text-sm">
                <span className="min-w-0 flex-1 truncate font-medium">{r.label}</span>
                {r.detail && (
                  <span className="hidden truncate text-xs text-muted-foreground sm:inline">
                    {r.detail}
                  </span>
                )}
                <span className="flex shrink-0 items-center">{r.actions}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
