// Exporting Raptor's own eggs (RPTR_v1, docs/EGGS.md#raptor-eggs) for
// other panels. No imports, so it's tested on its own.

// Raptor's settings: top-level in Raptor's eggs, x-raptor's keys in others
// (internal/eggs.RaptorKeys).
const raptorKeys = [
  "arch",
  "category",
  "library",
  "install",
  "backup",
  "health",
  "players",
  "addons",
  "workshop",
  "web",
  "modpack",
  "rcon",
];

// isRaptorFile reports whether an egg's file is one of Raptor's own.
export function isRaptorFile(text: string): boolean {
  try {
    const v = JSON.parse(text)?.meta?.version;
    return typeof v === "string" && v.startsWith("RPTR_");
  } catch {
    return false; // YAML: not a Raptor egg, which is always JSON
  }
}

// pterodactylFile turns one of Raptor's eggs into a Pterodactyl egg
// (PTDL_v2), which Pterodactyl and Pelican import: Raptor's settings go
// under x-raptor, which they ignore, and the first startup command becomes
// "startup". Key order is kept.
export function pterodactylFile(text: string): string {
  const egg = JSON.parse(text) as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  const ext: Record<string, unknown> = {};
  const commands = egg.startup_commands as Record<string, string> | undefined;
  for (const [k, v] of Object.entries(egg)) {
    if (raptorKeys.includes(k)) {
      ext[k] = v;
    } else if (k === "meta") {
      out.meta = { version: "PTDL_v2", update_url: null };
    } else if (k === "startup_commands") {
      if (!("startup" in egg) && commands) out.startup = Object.values(commands)[0] ?? "";
    } else if (k !== "x-raptor") {
      out[k] = v;
    }
  }
  if (Object.keys(ext).length > 0) out["x-raptor"] = ext;
  return `${JSON.stringify(out, null, 4)}\n`;
}
