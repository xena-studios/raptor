import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Ban, Loader2, LogOut, RefreshCw } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import {
  connectedFor,
  idHint,
  kickTakesReason,
  type RconGame,
  type RconPlayer,
  textLine,
  validId,
} from "@/lib/rcon-players";

type Manage = {
  op: "kick" | "ban" | "unban" | "say";
  id?: string;
  name?: string;
  reason?: string;
  message?: string;
};

// RconPlayers is a Steam game's Players tab (#423): who's on, from the
// game over RCON (Wings' players.online), with kick, ban, unban, and a
// message to everyone (players.manage). Wings connects to the game on
// the node and builds every command itself, so nothing but a checked
// player ID and one plain line of text reaches the game.
export function RconPlayers({
  nodeId,
  serverId,
  game,
  running,
  canPlayers,
}: {
  nodeId: string;
  serverId: string;
  game: RconGame;
  running: boolean;
  canPlayers: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const { confirm, prompt } = useConfirm();
  const key = ["players.online", nodeId, serverId];
  const online = useTanQuery({
    enabled: running && canPlayers,
    queryKey: key,
    queryFn: async () =>
      (await api.run<{ players: RconPlayer[] }>("players.online", {}))?.players ?? [],
    refetchInterval: 15_000,
    retry: false,
  });
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  async function run(label: string, params: Manage, done: string) {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await api.run("players.manage", params);
      setNote(done);
      // The game takes a moment to drop a kicked player.
      setTimeout(() => client.invalidateQueries({ queryKey: key }), 1_000);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  async function kick(p: RconPlayer) {
    let reason = "";
    if (kickTakesReason(game)) {
      const r = await prompt({
        title: `Kick ${p.name}?`,
        description: "They can join again right away. A reason is shown to them.",
        label: "Reason (optional)",
        maxLength: 100,
        confirm: "Kick",
      });
      if (r === null) return;
      reason = r;
    } else if (
      !(await confirm({
        title: `Kick ${p.name}?`,
        description: "They can join again right away.",
        confirm: "Kick",
      }))
    ) {
      return;
    }
    await run(`kick:${p.id}`, { op: "kick", id: p.id, reason }, `Kicked ${p.name}.`);
  }

  async function ban(p: RconPlayer) {
    const ok = await confirm({
      title: `Ban ${p.name}?`,
      description: `They're kicked and can't join again until they're unbanned by their ID (${p.id}).`,
      confirm: "Ban",
      destructive: true,
    });
    if (!ok) return;
    await run(`ban:${p.id}`, { op: "ban", id: p.id, name: p.name }, `Banned ${p.name} (${p.id}).`);
  }

  if (!canPlayers)
    return (
      <p className="text-sm text-muted-foreground">
        Seeing and managing players needs the Manage players permission.
      </p>
    );
  if (!running)
    return (
      <p className="text-sm text-muted-foreground">
        Start the server to see who's on. The Players tab talks to the game over RCON while it runs.
      </p>
    );

  const players = online.data ?? [];
  const disabled = !!busy;
  return (
    <div className="flex flex-col gap-6">
      {note && <p className="text-sm text-muted-foreground">{note}</p>}
      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-2">
          <div className="flex flex-col gap-1.5">
            <CardTitle>Online</CardTitle>
            <CardDescription>
              Who's on now, from the game over RCON. It refreshes every 15 seconds.
            </CardDescription>
          </div>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Refresh"
            title="Refresh"
            disabled={online.isFetching}
            onClick={() => online.refetch()}
          >
            <RefreshCw className={online.isFetching ? "animate-spin" : undefined} />
          </Button>
        </CardHeader>
        <CardContent>
          {online.error ? (
            <p className="text-sm text-destructive">{message(online.error)}</p>
          ) : online.isPending ? (
            <p className="text-sm text-muted-foreground">Asking the game…</p>
          ) : players.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nobody's on.</p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {players.map((p) => (
                <li key={p.id} className="flex items-center gap-3 px-3 py-1.5 text-sm">
                  <span className="min-w-0 flex-1 truncate font-medium">
                    {p.name || "(no name)"}
                    {p.bot && <span className="ml-2 text-xs text-muted-foreground">bot</span>}
                  </span>
                  <span className="hidden truncate font-mono text-xs text-muted-foreground sm:inline">
                    {p.id}
                  </span>
                  <span className="hidden w-24 text-right text-xs text-muted-foreground tabular-nums sm:inline">
                    {[p.ping ? `${p.ping} ms` : "", connectedFor(p.seconds)]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                  <span className="flex shrink-0 items-center">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Kick ${p.name}`}
                      title={`Kick ${p.name}`}
                      disabled={disabled}
                      onClick={() => kick(p)}
                    >
                      {busy === `kick:${p.id}` ? <Loader2 className="animate-spin" /> : <LogOut />}
                    </Button>
                    {!p.bot && (
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Ban ${p.name}`}
                        title={`Ban ${p.name}`}
                        disabled={disabled}
                        onClick={() => ban(p)}
                      >
                        {busy === `ban:${p.id}` ? <Loader2 className="animate-spin" /> : <Ban />}
                      </Button>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Message everyone</CardTitle>
          <CardDescription>
            Shown in the game's chat. One line, up to 120 characters.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <OneField
            label="Message"
            placeholder="Restarting in 5 minutes"
            button="Send"
            maxLength={120}
            disabled={disabled}
            valid={(v) => textLine(v, 120) !== ""}
            onSubmit={(v) => run("say", { op: "say", message: v }, "Sent.")}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Unban</CardTitle>
          <CardDescription>
            Let a banned player back in by their ID: the one shown when they were banned.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <OneField
            label="Player ID"
            placeholder={idHint[game]}
            button="Unban"
            mono
            disabled={disabled}
            valid={(v) => validId(game, v, true)}
            invalid={`That isn't a ${idHint[game].split(",")[0]}.`}
            onSubmit={(v) => run("unban", { op: "unban", id: v }, `Unbanned ${v}.`)}
          />
        </CardContent>
      </Card>
    </div>
  );
}

function OneField({
  label,
  placeholder,
  button,
  maxLength,
  mono,
  disabled,
  valid,
  invalid,
  onSubmit,
}: {
  label: string;
  placeholder: string;
  button: string;
  maxLength?: number;
  mono?: boolean;
  disabled: boolean;
  valid: (v: string) => boolean;
  invalid?: string;
  onSubmit: (v: string) => Promise<void>;
}) {
  const [value, setValue] = useState("");
  const v = value.trim();
  const ok = valid(v);
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!ok) return;
    await onSubmit(v);
    setValue("");
  }
  return (
    <form onSubmit={submit} className="flex flex-wrap items-start gap-2">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <Input
          aria-label={label}
          placeholder={placeholder}
          className={mono ? "font-mono" : undefined}
          spellCheck={!mono}
          autoComplete="off"
          maxLength={maxLength}
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        {v && !ok && invalid && <span className="text-xs text-destructive">{invalid}</span>}
      </div>
      <Button type="submit" variant="outline" disabled={disabled || !ok}>
        {button}
      </Button>
    </form>
  );
}
