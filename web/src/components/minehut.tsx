import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Loader2 } from "lucide-react";
import { useMemo, useState } from "react";

import { CopyButton, Hidden } from "@/components/address";
import { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import {
  type MinehutKind,
  minehutEdits,
  minehutFlags,
  minehutKind,
  minehutProxyType,
  withFlags,
  withoutFlags,
} from "@/lib/minehut";
import { base64, type ServerConfig, startupParams } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

const guide =
  "https://support.minehut.com/hc/en-us/articles/33252734474899-How-to-Setup-a-Minehut-External-Hosted-Server";

// Minehut connects a Minecraft server to Minehut as an external server
// (#455): it makes the changes Minehut's guide asks for (the config files,
// and the Java flags that send logins through Minehut, which change the
// startup command, so they're signed) and shows what to type into
// Minehut's External Server Settings. Undo puts it all back.
export function Minehut({
  orgId,
  nodeId,
  serverId,
  userId,
  host,
  port,
  canChange,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  // The node's hostname, for Minehut's IP field (it follows the IP).
  host: string;
  port?: number;
  // The startup permission and files.write.
  canChange: boolean;
}) {
  const client = useQueryClient();
  const api = useFilesApi(nodeId, serverId);
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const kind = minehutKind(cfg?.egg?.addons?.plugins?.loaders);
  const [dialog, setDialog] = useState<"on" | "off">();
  const [tcpShield, setTcpShield] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  if (!cfg || !kind) return null;
  if (kind === "bungeecord") {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Minehut</CardTitle>
          <CardDescription>
            Minehut doesn't take BungeeCord as an external server. Use Waterfall or Velocity
            (Settings → Server type) to connect it.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }
  const flags = minehutFlags[kind];
  const on = cfg.startup.includes(flags);

  // apply makes (or undoes) every change: the config files first, so a
  // file that isn't there yet stops it before anything changes, then the
  // signed startup change.
  async function apply(turnOn: boolean) {
    if (!cfg || !kind || kind === "bungeecord") return;
    setBusy(true);
    setError("");
    setDone("");
    try {
      const writes: { path: string; text: string }[] = [];
      for (const edit of minehutEdits(kind, tcpShield)) {
        let text: string;
        try {
          const file = await api.run<{ data: string }>("files.read", { path: edit.path });
          text = new TextDecoder().decode(fromBase64(file?.data ?? ""));
        } catch {
          throw new Error(
            `There's no ${edit.path} yet. Start the server once so it makes its files, then try again.`,
          );
        }
        const next = edit.apply(text, turnOn);
        if (next === undefined) {
          throw new Error(
            `${edit.path} doesn't have the setting Minehut needs. Is it the right server type?`,
          );
        }
        if (next !== text) writes.push({ path: edit.path, text: next });
      }
      const startup = turnOn ? withFlags(cfg.startup, flags) : withoutFlags(cfg.startup, flags);
      if (startup === undefined) {
        throw new Error(
          "Its startup command doesn't start Java the usual way, so the flags can't be added. Add them on Startup.",
        );
      }
      if (startup !== cfg.startup) {
        await sendSigned({
          userId,
          nodeId,
          action: "server.update",
          serverId,
          params: startupParams(cfg, res.data?.server?.name ?? "", startup),
        });
      }
      for (const w of writes) {
        await api.run("files.write", {
          path: w.path,
          data: base64(new TextEncoder().encode(w.text)),
        });
      }
      await client.invalidateQueries();
      setDialog(undefined);
      setDone(
        turnOn
          ? "Set up for Minehut. Restart the server, then fill in Minehut's settings below."
          : "Minehut's changes are undone. Restart the server to apply.",
      );
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Minehut</CardTitle>
        <CardDescription>
          List this server on Minehut as an external server: players join through Minehut's network
          and lobby, with its DDoS protection.{" "}
          <a
            href={guide}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 underline underline-offset-4"
          >
            Minehut's guide <ExternalLink className="size-3" />
          </a>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm">
        {on ? (
          <MinehutValues kind={kind} host={host} port={port} tcpShield={tcpShield} />
        ) : (
          <p className="text-muted-foreground">
            Raptor makes the changes Minehut asks for {stepsFor[kind]}, and shows what to enter in
            Minehut.
          </p>
        )}
        {canChange && (
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant={on ? "outline" : "default"}
              disabled={busy}
              onClick={() => setDialog(on ? "off" : "on")}
            >
              {on ? "Undo Minehut's changes" : "Set up for Minehut"}
            </Button>
          </div>
        )}
        {done && <p className="text-muted-foreground">{done}</p>}
        {error && !dialog && <p className="text-destructive">{error}</p>}
      </CardContent>
      {dialog && (
        <Dialog open onOpenChange={(o) => !o && !busy && setDialog(undefined)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {dialog === "on" ? "Set up for Minehut?" : "Undo Minehut's changes?"}
              </DialogTitle>
              <DialogDescription>
                {dialog === "on"
                  ? `Raptor will ${stepsFor[kind]}. Your passkey signs the startup change.`
                  : "Raptor puts the config files and startup command back. Your passkey signs the startup change."}
              </DialogDescription>
            </DialogHeader>
            {dialog === "on" && (
              <div className="flex flex-col gap-3 text-sm">
                <ul className="flex list-disc flex-col gap-1 pl-5 text-muted-foreground">
                  <li>
                    After this, players join <strong>only through Minehut</strong>: the server
                    expects Minehut's proxy in front of it, so joining it directly stops working,
                    and Raptor's player count may go blank.
                  </li>
                  <li>
                    Minehut takes new external servers from its Partners Program, and charges
                    credits to keep one active. Check your Minehut account first.
                  </li>
                </ul>
                <label className="flex items-center gap-2">
                  <Checkbox checked={tcpShield} onCheckedChange={(v) => setTcpShield(!!v)} />I use
                  TCPShield in front of it
                </label>
              </div>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={() => setDialog(undefined)}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={() => void apply(dialog === "on")}>
                {busy && <Loader2 className="animate-spin" />}
                {dialog === "on" ? "Set up" : "Undo"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </Card>
  );
}

const stepsFor: Record<Exclude<MinehutKind, "bungeecord">, string> = {
  velocity:
    "(turn on the HAProxy protocol in velocity.toml, and add Minehut's login flag to the startup command)",
  waterfall:
    "(turn on the proxy protocol in config.yml, and add Minehut's login flag to the startup command)",
  paper:
    "(turn on the proxy protocol in config/paper-global.yml, turn off enforce-secure-profile in server.properties, and add Minehut's login flags to the startup command)",
};

// MinehutValues are what Minehut's External Server Settings ask for.
function MinehutValues({
  kind,
  host,
  port,
  tcpShield,
}: {
  kind: Exclude<MinehutKind, "bungeecord">;
  host: string;
  port?: number;
  tcpShield: boolean;
}) {
  const rows: [string, string, boolean][] = [
    ["Proxy Type", minehutProxyType[kind], false],
    ["DNS Record Type", "Port", false],
    ["Port", String(port ?? ""), false],
    ["TCP Shield", tcpShield ? "Configured Externally" : "Not Configured", false],
  ];
  return (
    <div className="flex flex-col gap-1.5">
      <p>
        In Minehut, set the server's plan to <strong>External</strong>, then fill in{" "}
        <strong>External Server Settings</strong>:
      </p>
      <dl className="divide-y rounded-lg border">
        <div className="flex items-center gap-3 px-3 py-1.5">
          <dt className="w-32 shrink-0 text-muted-foreground">IP</dt>
          <dd className="min-w-0 flex-1">
            <Hidden value={host} label="address" />
          </dd>
        </div>
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center gap-3 px-3 py-1.5">
            <dt className="w-32 shrink-0 text-muted-foreground">{k}</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              <code className="truncate text-xs">{v}</code>
              <CopyButton value={v} label={k.toLowerCase()} />
            </dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-muted-foreground">
        The IP is the node's address, which follows its IP if that changes. With TCPShield, use the
        domain TCPShield gives you instead.
      </p>
    </div>
  );
}
