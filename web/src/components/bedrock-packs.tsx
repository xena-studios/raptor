import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Loader2, Package, Power, PowerOff, Trash2, Upload } from "lucide-react";
import { type ChangeEvent, useRef, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { type Content, type Entry, useFilesApi } from "@/components/file-manager/data";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  listFile,
  manifestOf,
  type PackKind,
  packDir,
  packFolder,
  packsIn,
  parseWorldPacks,
  withoutPack,
  withPack,
} from "@/lib/bedrock-packs";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { propertyValue } from "@/lib/minecraft-text";
import { base64 } from "@/lib/servers";
import { uploadFile } from "@/lib/upload";

type Listing = { path: string; entries: Entry[] };
type Installed = {
  uuid: string;
  name: string;
  version: number[];
  kind: PackKind;
  folder: string;
  experimental: boolean;
};

// Mojang's own packs, which come with the server.
const builtIn = /^(vanilla|chemistry|experimental|editor|server_editor)/i;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// BedrockPacks installs Bedrock behaviour and resource packs (#416): a
// .mcpack or .mcaddon is read in the browser (its manifests), unpacked
// into behavior_packs/ or resource_packs/, and put on the loaded world's
// list, which is what makes Bedrock load it.
export function BedrockPacks({
  nodeId,
  serverId,
  canWrite,
}: {
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const readText = async (path: string) => {
    try {
      const f = await api.run<Content>("files.read", { path });
      return new TextDecoder().decode(fromBase64(f?.data ?? ""));
    } catch {
      return undefined;
    }
  };
  const state = useTanQuery({
    queryKey: ["bedrock-packs", nodeId, serverId],
    queryFn: async () => {
      const props = (await readText("server.properties")) ?? "";
      const level = propertyValue(props, "level-name") || "Bedrock level";
      const world = `worlds/${level}`;
      const worldThere = !!(await api
        .run<Listing>("files.list", { path: world })
        .catch(() => undefined));
      const installed: Installed[] = [];
      for (const kind of ["behavior", "resource"] as const) {
        const l = await api
          .run<Listing>("files.list", { path: packDir[kind] })
          .catch(() => undefined);
        for (const d of (l?.entries ?? [])
          .filter((e) => e.type === "dir" && !builtIn.test(e.name))
          .slice(0, 60)) {
          const m = manifestOf((await readText(`${packDir[kind]}/${d.name}/manifest.json`)) ?? "");
          if (m) installed.push({ ...m, folder: d.name, kind });
        }
      }
      const on = {
        behavior: parseWorldPacks(await readText(`${world}/${listFile.behavior}`)),
        resource: parseWorldPacks(await readText(`${world}/${listFile.resource}`)),
      };
      return { level, world, worldThere, installed, on };
    },
  });
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const file = useRef<HTMLInputElement>(null);

  async function act(label: string, fn: () => Promise<void>, done: string) {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: ["bedrock-packs", nodeId, serverId] });
      setNote(done);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  async function setOn(kind: PackKind, p: { uuid: string; version: number[] }, on: boolean) {
    const s = state.data;
    if (!s) return;
    const text = on ? withPack(s.on[kind], p) : withoutPack(s.on[kind], p.uuid);
    await api.run("files.write", {
      path: `${s.world}/${listFile[kind]}`,
      data: base64(new TextEncoder().encode(text)),
    });
  }

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = "";
    const s = state.data;
    if (!f || !s) return;
    await act(
      "upload",
      async () => {
        if (!s.worldThere)
          throw new Error("The world isn't made yet: start the server once, then add packs.");
        const packs = await packsIn(new Uint8Array(await f.arrayBuffer()));
        if (packs.length === 0)
          throw new Error(
            "No packs in that file: a .mcpack or .mcaddon has a manifest.json for each.",
          );
        // Each archive once: a .mcaddon's packs may share one.
        const archives = [...new Set(packs.map((p) => p.archive))];
        for (const archive of archives) {
          const tmp = `raptor-pack-${Math.random().toString(36).slice(2, 10)}`;
          await uploadFile({
            nodeId,
            serverId,
            dir: "",
            file: new File([archive as BlobPart], `${tmp}.zip`),
            onProgress: () => {},
            signal: new AbortController().signal,
          });
          await api.run("files.decompress", { path: `${tmp}.zip`, dest: tmp });
          for (const p of packs.filter((x) => x.archive === archive)) {
            const from = p.root ? `${tmp}/${p.root}` : tmp;
            // Unpacking runs in the background: wait for the manifest.
            let ready = false;
            for (let i = 0; i < 60 && !ready; i++) {
              const l = await api.run<Listing>("files.list", { path: from }).catch(() => undefined);
              ready = !!l?.entries.some((x) => x.name.toLowerCase() === "manifest.json");
              if (!ready) await sleep(1000);
            }
            if (!ready)
              throw new Error(
                `${p.name} is taking long to unpack: check Files, then add it from there.`,
              );
            const folder = packFolder(p.name, p.uuid);
            await api.run("files.mkdir", { path: packDir[p.kind] }).catch(() => undefined);
            await api.run("files.rename", {
              dir: "",
              moves: [{ from, to: `${packDir[p.kind]}/${folder}` }],
            });
            const current = await readText(`${s.world}/${listFile[p.kind]}`);
            await api.run("files.write", {
              path: `${s.world}/${listFile[p.kind]}`,
              data: base64(new TextEncoder().encode(withPack(parseWorldPacks(current), p))),
            });
          }
          await api
            .run("files.delete", { dir: "", names: [`${tmp}.zip`, tmp] })
            .catch(() => undefined);
        }
        const exp = packs.some((p) => p.experimental);
        setNote(
          `Added ${packs.map((p) => p.name).join(", ")}. Restart the server to load ${packs.length === 1 ? "it" : "them"}.${exp ? " It uses scripts: turn on the world's Beta APIs experiment, or it won't run." : ""}`,
        );
      },
      "",
    );
  }

  async function remove(p: Installed) {
    const ok = await confirm({
      title: `Remove ${p.name}?`,
      description:
        "It comes off the world's list and its folder goes to the server's trash. Things in the world made by it may disappear.",
      confirm: "Remove",
      destructive: true,
    });
    if (!ok) return;
    await act(
      `remove:${p.uuid}`,
      async () => {
        await setOn(p.kind, p, false);
        await api.run("files.delete", { dir: packDir[p.kind], names: [p.folder] });
      },
      `${p.name} is removed. Restart the server to unload it.`,
    );
  }

  if (state.isPending) return <p className="text-sm text-muted-foreground">Reading packs…</p>;
  if (state.error || !state.data)
    return <p className="text-sm text-destructive">{message(state.error)}</p>;
  const s = state.data;
  const isOn = (p: Installed) => s.on[p.kind].some((x) => x.pack_id.toLowerCase() === p.uuid);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Behaviour and resource packs</CardTitle>
        <CardDescription>
          Packs for the world {s.level}. Add a .mcpack or .mcaddon: Raptor puts each pack where it
          goes and turns it on for the world. Restart the server to load changes.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {!s.worldThere && (
          <p className="text-muted-foreground">
            The world isn't made yet: start the server once, then add packs.
          </p>
        )}
        {s.installed.length === 0 ? (
          <p className="text-muted-foreground">No packs yet.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {s.installed.map((p) => (
              <li
                key={`${p.kind}:${p.uuid}`}
                className="flex flex-wrap items-center gap-3 px-3 py-2"
              >
                <Package className="size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 basis-40">
                  <span className="font-medium">{p.name}</span>{" "}
                  <span className="font-mono text-xs text-muted-foreground">
                    {p.version.join(".")}
                  </span>
                </span>
                <Badge variant="outline">{p.kind === "behavior" ? "Behaviour" : "Resource"}</Badge>
                {p.experimental && <Badge variant="outline">Needs Beta APIs</Badge>}
                {isOn(p) ? <Badge>On</Badge> : <Badge variant="secondary">Off</Badge>}
                {canWrite && (
                  <>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={isOn(p) ? `Turn ${p.name} off` : `Turn ${p.name} on`}
                      title={isOn(p) ? "Turn off for the world" : "Turn on for the world"}
                      disabled={!!busy || !s.worldThere}
                      onClick={() =>
                        void act(
                          `toggle:${p.uuid}`,
                          () => setOn(p.kind, p, !isOn(p)),
                          "Saved. Restart the server to apply it.",
                        )
                      }
                    >
                      {isOn(p) ? <PowerOff /> : <Power />}
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove ${p.name}`}
                      className="text-muted-foreground hover:text-destructive"
                      disabled={!!busy}
                      onClick={() => void remove(p)}
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
          <div className="flex items-center gap-2">
            <input
              ref={file}
              type="file"
              accept=".mcpack,.mcaddon,.zip"
              className="hidden"
              onChange={(e) => void upload(e)}
            />
            <Button
              size="sm"
              variant="outline"
              disabled={!!busy || !s.worldThere}
              onClick={() => file.current?.click()}
            >
              {busy === "upload" ? <Loader2 className="animate-spin" /> : <Upload />} Add a pack
            </Button>
          </div>
        )}
        {note && <p className="text-muted-foreground">{note}</p>}
        {error && <p className="text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
