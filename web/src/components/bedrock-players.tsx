import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useMemo, useState } from "react";

import { CopyButton } from "@/components/address";
import { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import {
  floodgateSource,
  geyserConfig,
  geyserPlatform,
  hasSamePort,
  isProxy,
  samePort,
} from "@/lib/geyser";
import { base64, type ServerConfig } from "@/lib/servers";
import { addonClient } from "@/lib/transport";

type Listing = { dir: string; files: { name: string }[] };

// BedrockPlayers lets Bedrock players (phones, consoles, Windows) join a
// Java server (#419): it installs Geyser (and Floodgate, so they needn't
// own Java), each from its source and checked by its hash like any add-on,
// then, once Geyser has made its config on the next start, puts Bedrock
// on the server's own port. Every Raptor port is published for TCP and
// UDP, so Bedrock players use the same address and port as Java ones.
export function BedrockPlayers({
  orgId,
  nodeId,
  serverId,
  address,
  port,
  canChange,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  // What players connect to (shown address, or the node's hostname).
  address: string;
  port?: number;
  // files.write.
  canChange: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const kind = cfg?.egg?.addons?.plugins ? "plugins" : cfg?.egg?.addons?.mods ? "mods" : "";
  const addon = kind ? cfg?.egg?.addons?.[kind] : undefined;
  const platform = addon ? geyserPlatform(kind, addon.loaders) : undefined;
  const list = useTanQuery({
    queryKey: ["addons", nodeId, serverId, kind],
    queryFn: () => api.run<Listing>("addons.list", { kind }),
    enabled: !!platform,
    retry: false,
  });
  const dir = list.data?.dir ?? addon?.dir ?? "plugins";
  const installed = list.data?.files.some((f) => /^geyser/i.test(f.name)) ?? false;
  const configPath = platform ? geyserConfig(platform, dir) : "";
  const config = useTanQuery({
    queryKey: ["file", nodeId, serverId, configPath],
    queryFn: async () => {
      const f = await api.run<{ data: string }>("files.read", { path: configPath });
      return new TextDecoder().decode(fromBase64(f?.data ?? ""));
    },
    enabled: installed && !!configPath,
    retry: false,
  });
  const [floodgate, setFloodgate] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  if (!platform || !port) return null;
  const proxy = isProxy(platform);
  const ready = config.data !== undefined && hasSamePort(config.data, platform, port);

  // install puts Geyser (and Floodgate) in, newest first from each source.
  async function install() {
    if (!platform || !addon) return;
    setBusy(true);
    setError("");
    setNote("");
    try {
      const wanted = [{ source: "modrinth", project: "geyser", name: "Geyser" }];
      if (floodgate && !proxy) wanted.push({ ...floodgateSource(platform), name: "Floodgate" });
      for (const w of wanted) {
        const v = await addonClient.listAddonVersions({
          orgId,
          projectId: w.project,
          loaders: addon.loaders,
          gameVersion: "",
          source: w.source,
        });
        const file = v.versions[0]?.file;
        if (!file) throw new Error(`${w.name} has no version for this server's software.`);
        await api.run("addons.install", {
          kind,
          url: file.url,
          sha512: file.sha512 || undefined,
          sha256: file.sha256 || undefined,
          filename: file.filename,
        });
      }
      await client.invalidateQueries({ queryKey: ["addons", nodeId, serverId, kind] });
      setNote(
        "Installed. Restart the server: Geyser makes its settings on its first start, then come back here to finish.",
      );
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  // finish puts Bedrock on the server's own port in Geyser's config.
  async function finish() {
    if (!platform || config.data === undefined || !port) return;
    setBusy(true);
    setError("");
    setNote("");
    try {
      const next = samePort(config.data, platform, port);
      if (next === undefined) throw new Error(`${configPath} doesn't look like Geyser's settings.`);
      await api.run("files.write", {
        path: configPath,
        data: base64(new TextEncoder().encode(next)),
      });
      await client.invalidateQueries({ queryKey: ["file", nodeId, serverId, configPath] });
      setNote("Done. Restart the server, and Bedrock players can join.");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Bedrock players</CardTitle>
        <CardDescription>
          Let players on phones, consoles, and Windows (Minecraft: Bedrock Edition) join this Java
          server, with Geyser.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {ready ? (
          <>
            <p>Bedrock players join with this address and port (Servers → Add Server):</p>
            <dl className="divide-y rounded-lg border">
              {(
                [
                  ["Address", address],
                  ["Port", String(port)],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex items-center gap-3 px-3 py-1.5">
                  <dt className="w-20 shrink-0 text-muted-foreground">{k}</dt>
                  <dd className="flex min-w-0 flex-1 items-center gap-1">
                    <code className="truncate text-xs">{v}</code>
                    <CopyButton value={v} label={k.toLowerCase()} />
                  </dd>
                </div>
              ))}
            </dl>
            <p className="text-xs text-muted-foreground">
              Same as Java's, over UDP. If they can't connect, check your provider's firewall lets
              UDP through on port {port}.
            </p>
          </>
        ) : !installed ? (
          <>
            <p className="text-muted-foreground">
              Installs Geyser{proxy ? "" : " and Floodgate"} from{" "}
              {platform === "Fabric" || platform === "NeoForge"
                ? "Modrinth"
                : "Modrinth and Hangar"}
              , each checked like any add-on. Bedrock players then use the same address and port as
              Java players.
            </p>
            {!proxy ? (
              <label className="flex items-center gap-2">
                <Checkbox checked={floodgate} onCheckedChange={(v) => setFloodgate(!!v)} />
                Floodgate: Bedrock players don't need a Java account
              </label>
            ) : (
              <p className="text-xs text-muted-foreground">
                On a proxy, Floodgate also needs its key on every server behind it, so it isn't
                installed here: add it by hand if you want it.
              </p>
            )}
            {canChange && (
              <Button
                size="sm"
                className="self-start"
                disabled={busy}
                onClick={() => void install()}
              >
                {busy && <Loader2 className="animate-spin" />} Let Bedrock players join
              </Button>
            )}
          </>
        ) : config.isPending ? (
          <p className="text-muted-foreground">Checking Geyser's settings…</p>
        ) : config.isError ? (
          <p className="text-muted-foreground">
            Geyser is installed. Restart the server so it makes its settings, then finish here.
          </p>
        ) : (
          <>
            <p className="text-muted-foreground">
              Geyser is installed. One more step puts Bedrock players on this server's own port (
              {port}), so they use the same address as Java players.
            </p>
            {canChange && (
              <Button
                size="sm"
                className="self-start"
                disabled={busy}
                onClick={() => void finish()}
              >
                {busy && <Loader2 className="animate-spin" />} Finish setting up
              </Button>
            )}
          </>
        )}
        {note && <p className="text-muted-foreground">{note}</p>}
        {error && <p className="text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
