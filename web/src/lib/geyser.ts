// Letting Bedrock players join a Java server with Geyser (#419): which
// Geyser build a server takes, where its config is, and the one change
// Raptor makes to it.

export type GeyserPlatform = "Spigot" | "Velocity" | "BungeeCord" | "Fabric" | "NeoForge";

// geyserPlatform is the Geyser build for a server, from its egg's add-on
// kind and loaders (the first is the software itself); undefined when
// Geyser has none (Forge, Quilt, Sponge).
export function geyserPlatform(kind: string, loaders: string[]): GeyserPlatform | undefined {
  const first = loaders[0];
  if (kind === "plugins") {
    if (first === "velocity") return "Velocity";
    if (first === "waterfall" || first === "bungeecord") return "BungeeCord";
    if (["paper", "purpur", "folia", "spigot", "bukkit"].includes(first ?? "")) return "Spigot";
  }
  if (kind === "mods") {
    if (first === "fabric") return "Fabric";
    if (first === "neoforge") return "NeoForge";
  }
  return undefined;
}

// isProxy: Floodgate on a proxy also needs its key on every backend,
// which Raptor doesn't set up.
export function isProxy(p: GeyserPlatform): boolean {
  return p === "Velocity" || p === "BungeeCord";
}

// geyserConfig is where Geyser writes its config on its first start.
// Plugins go in the plugins folder (dir), mods' configs in config/.
export function geyserConfig(p: GeyserPlatform, dir: string): string {
  return p === "Fabric" || p === "NeoForge"
    ? `config/Geyser-${p}/config.yml`
    : `${dir}/Geyser-${p}/config.yml`;
}

// floodgateSource is where Floodgate's build for a platform comes from:
// Hangar has the plugin, Modrinth the mods.
export function floodgateSource(p: GeyserPlatform): { source: string; project: string } {
  return p === "Fabric" || p === "NeoForge"
    ? { source: "modrinth", project: "floodgate" }
    : { source: "hangar", project: "Floodgate" };
}

// The bedrock section's lines, up to the next top-level key.
function bedrockSection(text: string): { start: number; end: number } | undefined {
  const m = /^bedrock:[ \t]*\r?\n/m.exec(text);
  if (!m) return undefined;
  const start = m.index + m[0].length;
  const next = /^\S/m.exec(text.slice(start));
  return { start, end: next ? start + next.index : text.length };
}

// samePort is Geyser's config with Bedrock players on the server's own
// port, over UDP (every Raptor port is published for both): plugins clone
// the Java port at each start (clone-remote-port), mods are given it
// (port). Undefined when the config doesn't look like Geyser's.
export function samePort(text: string, p: GeyserPlatform, port: number): string | undefined {
  const sec = bedrockSection(text);
  if (!sec) return undefined;
  let body = text.slice(sec.start, sec.end);
  if (p === "Fabric" || p === "NeoForge") {
    if (!/^[ \t]+port:[ \t]*\d+/m.test(body)) return undefined;
    body = body.replace(/^([ \t]+port:[ \t]*)\d+/m, `$1${port}`);
  } else {
    if (/^[ \t]+clone-remote-port:/m.test(body)) {
      body = body.replace(/^([ \t]+clone-remote-port:[ \t]*)(true|false)/m, "$1true");
    } else {
      const indent = /^([ \t]+)\S/m.exec(body)?.[1] ?? "  ";
      body = `${body.replace(/\s*$/, "\n")}${indent}clone-remote-port: true\n`;
    }
  }
  return text.slice(0, sec.start) + body + text.slice(sec.end);
}

// hasSamePort reports whether the config already does.
export function hasSamePort(text: string, p: GeyserPlatform, port: number): boolean {
  return samePort(text, p, port) === text;
}
