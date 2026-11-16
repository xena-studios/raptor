import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { ImageUp, Loader2, Trash2 } from "lucide-react";
import { type ChangeEvent, useRef, useState } from "react";
import type { Content } from "@/components/file-manager/data";
import { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import { colours, motdLines, plainLength, propertyValue, withProperty } from "@/lib/minecraft-text";
import { base64, type ServerConfig } from "@/lib/servers";

// The server list shows about this many characters a line.
const lineLength = 45;

const formats: [code: string, label: string, title: string][] = [
  ["l", "B", "Bold"],
  ["o", "I", "Italic"],
  ["n", "U", "Underline"],
  ["m", "S", "Strikethrough"],
  ["r", "↺", "Reset to plain"],
];

// ServerListLook edits how a Minecraft Java server looks in players'
// server list (#425): its icon (server-icon.png, 64×64) and its MOTD (in
// server.properties), with a preview. Shown when the server has a
// server.properties; changes apply at the next restart.
export function ServerListLook({
  orgId,
  nodeId,
  serverId,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  // Minecraft Java servers: their egg's player query is Minecraft's.
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const java =
    !!res.data?.configJson &&
    (JSON.parse(res.data.configJson) as ServerConfig).egg?.players === "minecraft";
  const props = useTanQuery({
    queryKey: api.contentKey("server.properties"),
    queryFn: () => api.run<Content>("files.read", { path: "server.properties" }),
    enabled: java,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const icon = useTanQuery({
    queryKey: api.contentKey("server-icon.png"),
    queryFn: () => api.run<Content>("files.read", { path: "server-icon.png" }),
    enabled: java,
    retry: false,
    refetchOnWindowFocus: false,
  });
  // Without a server.properties (a proxy, or not started yet) there's
  // nothing to edit.
  if (!java || !props.data?.data) return null;
  const text = new TextDecoder().decode(fromBase64(props.data.data));
  return (
    <LookForm
      key={props.data.data}
      text={text}
      iconData={icon.data?.data}
      canWrite={canWrite}
      save={async (next, png) => {
        if (next !== undefined) {
          await api.run("files.write", {
            path: "server.properties",
            data: base64(new TextEncoder().encode(next)),
          });
        }
        if (png === null) {
          await api.run("files.delete", { dir: "", names: ["server-icon.png"] });
        } else if (png) {
          await api.run("files.write", { path: "server-icon.png", data: base64(png) });
        }
        await client.invalidateQueries({ queryKey: ["file", nodeId, serverId] });
      }}
    />
  );
}

function LookForm({
  text,
  iconData,
  canWrite,
  save,
}: {
  text: string;
  iconData?: string;
  canWrite: boolean;
  // undefined leaves it; png null removes the icon.
  save: (next: string | undefined, png: Uint8Array | null | undefined) => Promise<void>;
}) {
  const initial = propertyValue(text, "motd") ?? "A Minecraft Server";
  const [motd, setMotd] = useState(initial);
  const [png, setPng] = useState<Uint8Array | null>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const file = useRef<HTMLInputElement>(null);
  const shownIcon =
    png === null
      ? undefined
      : png
        ? `data:image/png;base64,${base64(png)}`
        : iconData
          ? `data:image/png;base64,${iconData}`
          : undefined;
  const lines = motd.split("\n");
  const dirty = motd !== initial || png !== undefined;

  // insert puts a § code at the cursor (or around the selection's start).
  function insert(code: string) {
    const el = box.current;
    const at = el?.selectionStart ?? motd.length;
    const next = `${motd.slice(0, at)}§${code}${motd.slice(at)}`;
    setMotd(next);
    setSaved(false);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(at + 2, at + 2);
    });
  }

  async function pick(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = "";
    if (!f) return;
    setError("");
    try {
      setPng(await squareIcon(f));
      setSaved(false);
    } catch {
      setError("That image couldn't be read. Try a PNG or JPEG.");
    }
  }

  async function submit() {
    setBusy(true);
    setError("");
    try {
      await save(motd !== initial ? withProperty(text, "motd", motd) : undefined, png);
      setSaved(true);
      setPng(undefined);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>In the server list</CardTitle>
        <CardDescription>
          The icon and message players see before they join. Changes show after the next restart.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Preview icon={shownIcon} motd={motd} />
        <div className="flex flex-wrap items-center gap-2">
          <input
            ref={file}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(e) => void pick(e)}
          />
          {canWrite && (
            <>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => file.current?.click()}
              >
                <ImageUp /> {shownIcon ? "Change icon" : "Add an icon"}
              </Button>
              {shownIcon && (
                <Button size="sm" variant="ghost" disabled={busy} onClick={() => setPng(null)}>
                  <Trash2 /> Remove icon
                </Button>
              )}
              <span className="text-xs text-muted-foreground">
                Any image: it's cropped square and made 64×64.
              </span>
            </>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="motd">Message (MOTD)</Label>
          {canWrite && (
            <div className="flex flex-wrap gap-1" role="toolbar" aria-label="Colours and styles">
              {Object.entries(colours).map(([code, c]) => (
                <button
                  key={code}
                  type="button"
                  title={c.name}
                  aria-label={c.name}
                  className="size-6 rounded border border-border"
                  style={{ background: c.hex }}
                  onClick={() => insert(code)}
                />
              ))}
              {formats.map(([code, label, title]) => (
                <Button
                  key={code}
                  type="button"
                  size="icon-xs"
                  variant="outline"
                  title={title}
                  aria-label={title}
                  className={
                    code === "l"
                      ? "font-bold"
                      : code === "o"
                        ? "italic"
                        : code === "n"
                          ? "underline"
                          : code === "m"
                            ? "line-through"
                            : ""
                  }
                  onClick={() => insert(code)}
                >
                  {label}
                </Button>
              ))}
            </div>
          )}
          <textarea
            id="motd"
            ref={box}
            rows={2}
            readOnly={!canWrite}
            spellCheck={false}
            className="min-h-16 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 font-mono text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
            value={motd}
            onChange={(e) => {
              setMotd(e.target.value);
              setSaved(false);
            }}
          />
          <p className="text-xs text-muted-foreground">
            Two lines at most. A colour button adds a code like §6 where the cursor is; colours turn
            styles off, so add a style after its colour.
            {lines.length > 2 && " Only the first two lines show."}
            {lines.slice(0, 2).some((l) => plainLength(l) > lineLength) &&
              ` Lines longer than about ${lineLength} characters get cut off.`}
          </p>
        </div>
        {canWrite && (
          <div className="flex items-center gap-3">
            <Button size="sm" disabled={busy || !dirty} onClick={() => void submit()}>
              {busy && <Loader2 className="animate-spin" />} Save
            </Button>
            {saved && !dirty && (
              <span className="text-sm text-muted-foreground">
                Saved. Restart the server to show it.
              </span>
            )}
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

// Preview draws a server-list entry: the icon and the MOTD's two lines.
function Preview({ icon, motd }: { icon?: string; motd: string }) {
  return (
    <div className="flex items-start gap-3 rounded-lg bg-zinc-900 p-2.5 font-mono text-sm leading-snug text-zinc-300">
      {icon ? (
        <img
          src={icon}
          alt="Server icon"
          className="size-16 shrink-0 [image-rendering:pixelated]"
        />
      ) : (
        <span className="flex size-16 shrink-0 items-center justify-center bg-zinc-800 text-xs text-zinc-500">
          No icon
        </span>
      )}
      <div className="min-w-0">
        <p className="text-white">A Minecraft Server</p>
        {motdLines(motd).map((line, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: two fixed lines
          <p key={i} className="truncate">
            {line.map((s, j) => (
              <span
                // biome-ignore lint/suspicious/noArrayIndexKey: runs in order
                key={j}
                style={{ color: s.colour }}
                className={[
                  s.bold && "font-bold",
                  s.italic && "italic",
                  s.underline && "underline",
                  s.strike && "line-through",
                ]
                  .filter(Boolean)
                  .join(" ")}
              >
                {s.obfuscated ? s.text.replace(/\S/g, "▒") : s.text}
              </span>
            ))}
          </p>
        ))}
      </div>
    </div>
  );
}

// squareIcon makes any image a 64×64 PNG, cropped to its middle square.
async function squareIcon(f: File): Promise<Uint8Array> {
  const img = await createImageBitmap(f);
  const side = Math.min(img.width, img.height);
  const canvas = document.createElement("canvas");
  canvas.width = 64;
  canvas.height = 64;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("no canvas");
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(img, (img.width - side) / 2, (img.height - side) / 2, side, side, 0, 0, 64, 64);
  const blob = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, "image/png"));
  if (!blob) throw new Error("no png");
  return new Uint8Array(await blob.arrayBuffer());
}
