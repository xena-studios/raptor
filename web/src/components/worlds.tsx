import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Archive, Check, Globe, Loader2, RotateCcw, Trash2, Upload } from "lucide-react";
import { type ChangeEvent, useRef, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { type Content, type Entry, useFilesApi } from "@/components/file-manager/data";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { propertyValue, withProperty } from "@/lib/minecraft-text";
import { gameRules, readGzipNbt } from "@/lib/nbt";
import { base64 } from "@/lib/servers";
import { commandClient } from "@/lib/transport";
import { uploadFile } from "@/lib/upload";
import {
  findWorlds,
  folderName,
  knownRules,
  validRule,
  validRuleValue,
  type World,
} from "@/lib/worlds";

type Listing = { path: string; entries: Entry[] };

const parentOf = (p: string) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
const baseOf = (p: string) => p.slice(p.lastIndexOf("/") + 1);

// Worlds lists a Minecraft server's worlds and does what people otherwise
// do in the file manager (#426): switch the one it loads, download one,
// upload one, start a new one, delete one (to the trash), and see and
// change its game rules.
export function Worlds({
  nodeId,
  serverId,
  running,
  canWrite,
  canCommand,
}: {
  nodeId: string;
  serverId: string;
  running: boolean;
  canWrite: boolean;
  canCommand: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const props = useTanQuery({
    queryKey: api.contentKey("server.properties"),
    queryFn: async () => {
      const f = await api.run<Content>("files.read", { path: "server.properties" });
      return new TextDecoder().decode(fromBase64(f?.data ?? ""));
    },
    retry: false,
  });
  // Folders with a level.dat: the server's root, and one level in (an
  // uploaded zip often has its world in a folder of its own).
  const scan = useTanQuery({
    queryKey: ["worlds", nodeId, serverId],
    queryFn: async () => {
      const root = await api.run<Listing>("files.list", { path: "" });
      const dirs = (root?.entries ?? []).filter((e) => e.type === "dir" && !e.denied).slice(0, 40);
      const out: Listing[] = [];
      for (const d of dirs) {
        const l = await api.run<Listing>("files.list", { path: d.name }).catch(() => undefined);
        if (!l) continue;
        out.push({ path: d.name, entries: l.entries });
        const has = l.entries.some((e) => e.name === "level.dat");
        const subdirs = l.entries.filter((e) => e.type === "dir");
        if (!has && subdirs.length > 0 && subdirs.length <= 4) {
          for (const s of subdirs) {
            const n = await api
              .run<Listing>("files.list", { path: `${d.name}/${s.name}` })
              .catch(() => undefined);
            if (n) out.push({ path: `${d.name}/${s.name}`, entries: n.entries });
          }
        }
      }
      return findWorlds(out);
    },
  });
  const active = propertyValue(props.data ?? "", "level-name") ?? "world";
  const rules = useTanQuery({
    queryKey: ["file", nodeId, serverId, `${active}/level.dat`],
    queryFn: async () => {
      const f = await api.run<Content>("files.read", { path: `${active}/level.dat` });
      return gameRules(await readGzipNbt(fromBase64(f?.data ?? "")));
    },
    enabled: !!props.data,
    retry: false,
  });
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [seed, setSeed] = useState("");
  const [allRules, setAllRules] = useState(false);
  const file = useRef<HTMLInputElement>(null);

  async function act(label: string, fn: () => Promise<unknown>, done: string) {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await fn();
      await client.invalidateQueries({ queryKey: ["worlds", nodeId, serverId] });
      await client.invalidateQueries({ queryKey: api.contentKey("server.properties") });
      setNote(done);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  const folders = (w: World) => [baseOf(w.path), ...w.dimensions.map(baseOf)];

  const switchTo = (w: World) =>
    act(
      `switch:${w.path}`,
      () =>
        api.run("files.write", {
          path: "server.properties",
          data: base64(
            new TextEncoder().encode(withProperty(props.data ?? "", "level-name", w.path)),
          ),
        }),
      `The server loads ${w.path} from its next start.${running ? " Restart it to switch now." : ""}`,
    );

  const download = (w: World) =>
    act(
      `download:${w.path}`,
      () =>
        api.run("files.compress", {
          dir: parentOf(w.path),
          names: folders(w),
          format: "zip",
          name: `${baseOf(w.path)}-${new Date().toISOString().slice(0, 10)}`,
        }),
      `Zipping ${w.path}. The zip appears in Files when it's ready, to download from there.`,
    );

  async function remove(w: World) {
    const ok = await confirm({
      title: `Delete ${w.path}?`,
      description: `${folders(w).join(", ")} ${folders(w).length === 1 ? "goes" : "go"} to the server's trash, where you can get ${folders(w).length === 1 ? "it" : "them"} back for a while.`,
      confirm: "Delete",
      destructive: true,
    });
    if (!ok) return;
    await act(
      `delete:${w.path}`,
      () => api.run("files.delete", { dir: parentOf(w.path), names: folders(w) }),
      `${w.path} is in the trash.`,
    );
  }

  async function reset(w: World) {
    const ok = await confirm({
      title: "Start a new world?",
      description: `${w.path} goes to the server's trash, and the server makes a new world when it starts${seed.trim() ? ` from the seed ${seed.trim()}` : ""}. Players' items and progress in it go with it.`,
      confirm: "Start a new world",
      destructive: true,
    });
    if (!ok) return;
    await act(
      "reset",
      async () => {
        await api.run("files.delete", { dir: parentOf(w.path), names: folders(w) });
        if (seed.trim()) {
          await api.run("files.write", {
            path: "server.properties",
            data: base64(
              new TextEncoder().encode(withProperty(props.data ?? "", "level-seed", seed.trim())),
            ),
          });
        }
      },
      "Done. Start the server to make the new world.",
    );
  }

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = "";
    if (!f) return;
    const dest = folderName(f.name);
    await act(
      "upload",
      async () => {
        await uploadFile({
          nodeId,
          serverId,
          dir: "",
          file: f,
          onProgress: () => {},
          signal: new AbortController().signal,
        });
        await api.run("files.decompress", { path: f.name, dest });
      },
      `Uploaded. It's being unpacked into ${dest}/; it shows here when that's done (refresh). The zip stays in Files.`,
    );
  }

  async function setRule(name: string, value: string) {
    if (!validRule(name) || !validRuleValue(value)) return;
    await act(
      `rule:${name}`,
      () =>
        commandClient.execute({
          nodeId,
          action: "server.command",
          serverId,
          paramsJson: JSON.stringify({ command: `gamerule ${name} ${value}` }),
        }),
      `Set ${name} to ${value}. It's saved with the world at its next save.`,
    );
  }

  if (scan.isPending || props.isPending)
    return <p className="text-sm text-muted-foreground">Looking for worlds…</p>;
  const worlds = scan.data ?? [];
  const activeWorld = worlds.find((w) => w.path === active);
  const shown = Object.entries(rules.data ?? {}).filter(
    ([k]) => allRules || k in knownRules || k.replace(/^minecraft:/, "") in knownRules,
  );

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Worlds</CardTitle>
          <CardDescription>
            The world the server loads is set by level-name in its settings; changing it here takes
            effect at the next start.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {worlds.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No worlds yet. The server makes {active} when it starts.
            </p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {worlds.map((w) => (
                <li key={w.path} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
                  <Globe className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 basis-40">
                    <span className="font-medium">{w.path}</span>
                    {w.dimensions.length > 0 && (
                      <span className="text-xs text-muted-foreground">
                        {" "}
                        with the Nether and the End
                      </span>
                    )}
                  </span>
                  {w.path === active && <Badge>Loaded</Badge>}
                  {canWrite && w.path !== active && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!!busy}
                      onClick={() => void switchTo(w)}
                    >
                      <Check /> Load this one
                    </Button>
                  )}
                  {canWrite && (
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Zip ${w.path} to download`}
                      title="Zip to download"
                      disabled={!!busy}
                      onClick={() => void download(w)}
                    >
                      {busy === `download:${w.path}` ? (
                        <Loader2 className="animate-spin" />
                      ) : (
                        <Archive />
                      )}
                    </Button>
                  )}
                  {canWrite && w.path !== active && (
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Delete ${w.path}`}
                      className="text-muted-foreground hover:text-destructive"
                      disabled={!!busy}
                      onClick={() => void remove(w)}
                    >
                      <Trash2 />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
          {canWrite && (
            <div className="flex flex-wrap items-center gap-2">
              <input
                ref={file}
                type="file"
                accept=".zip,.tar.gz,.tgz,.tar"
                className="hidden"
                onChange={(e) => void upload(e)}
              />
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                onClick={() => file.current?.click()}
              >
                {busy === "upload" ? <Loader2 className="animate-spin" /> : <Upload />} Upload a
                world
              </Button>
              <span className="text-xs text-muted-foreground">
                A zip of a world folder (the one with level.dat).
              </span>
            </div>
          )}
          {note && <p className="text-sm text-muted-foreground">{note}</p>}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
      </Card>

      {canWrite && activeWorld && (
        <Card>
          <CardHeader>
            <CardTitle>Start a new world</CardTitle>
            <CardDescription>
              {active} goes to the trash and the server makes a fresh world at its next start.
              {running && " Stop the server first."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap items-end gap-2">
            <Input
              aria-label="Seed"
              placeholder="Seed (optional)"
              className="w-56"
              maxLength={64}
              value={seed}
              onChange={(e) => setSeed(e.target.value)}
            />
            <Button
              variant="destructive"
              disabled={!!busy || running}
              onClick={() => void reset(activeWorld)}
            >
              {busy === "reset" ? <Loader2 className="animate-spin" /> : <RotateCcw />} Start a new
              world
            </Button>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Game rules</CardTitle>
          <CardDescription>
            {active}'s rules, as of its last save.{" "}
            {running
              ? canCommand
                ? "Changes are made through the console, right away."
                : "Changing them needs the Send console commands permission."
              : "Start the server to change them."}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {rules.isPending ? (
            <p className="text-sm text-muted-foreground">Reading {active}…</p>
          ) : !rules.data || Object.keys(rules.data).length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No rules to show yet: start the server once so it saves the world.
            </p>
          ) : (
            <>
              <ul className="divide-y rounded-lg border">
                {shown.map(([k, v]) => (
                  <RuleRow
                    key={k}
                    name={k}
                    value={v}
                    help={knownRules[k] ?? knownRules[k.replace(/^minecraft:/, "")]}
                    disabled={!running || !canCommand || !!busy}
                    onSet={(val) => void setRule(k, val)}
                  />
                ))}
              </ul>
              <label className="flex items-center gap-2 text-sm text-muted-foreground">
                <Checkbox checked={allRules} onCheckedChange={(on) => setAllRules(!!on)} />
                Show every rule
              </label>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function RuleRow({
  name,
  value,
  help,
  disabled,
  onSet,
}: {
  name: string;
  value: string;
  help?: string;
  disabled: boolean;
  onSet: (v: string) => void;
}) {
  const bool = value === "true" || value === "false";
  const [draft, setDraft] = useState(value);
  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
      <span className="min-w-0 flex-1 basis-48">
        <span className="font-mono text-xs">{name}</span>
        {help && <span className="block text-xs text-muted-foreground">{help}</span>}
      </span>
      {bool ? (
        <Button
          size="sm"
          variant="outline"
          disabled={disabled}
          onClick={() => onSet(value === "true" ? "false" : "true")}
        >
          {value === "true" ? "On" : "Off"} → turn {value === "true" ? "off" : "on"}
        </Button>
      ) : (
        <form
          className="flex items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            if (validRuleValue(draft.trim())) onSet(draft.trim());
          }}
        >
          <Input
            aria-label={name}
            className="h-8 w-24"
            value={draft}
            disabled={disabled}
            onChange={(e) => setDraft(e.target.value)}
          />
          <Button
            size="sm"
            variant="outline"
            type="submit"
            disabled={disabled || draft.trim() === value || !validRuleValue(draft.trim())}
          >
            Set
          </Button>
        </form>
      )}
    </li>
  );
}
