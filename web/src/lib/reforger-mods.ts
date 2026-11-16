// Arma Reforger's mods (#417, docs/EGGS.md#arma-reforger-mods): the
// game.mods list in the server's JSON config, which the server downloads
// from the Bohemia Workshop at start. These read and write that list and
// leave the rest of the config as it is.

// ConfigMod is one of game.mods. Keys Raptor doesn't know ("required",
// newer ones) are kept.
export type ConfigMod = {
  modId: string;
  name: string;
  // A pinned version; without one the server takes the newest.
  version?: string;
  [key: string]: unknown;
};

type Json = Record<string, unknown>;

export type ParsedConfig = { ok: true; mods: ConfigMod[] } | { ok: false; error: string };

const isObject = (v: unknown): v is Json =>
  typeof v === "object" && v !== null && !Array.isArray(v);

// A mod's ID: 16 hexadecimal digits.
const idPattern = /^[0-9A-F]{16}$/;

// parseConfig reads game.mods from a config's text. A config without a
// game block or a mods list has no mods; one that isn't JSON, or whose
// mods aren't a list, is an error (saving would lose what's there).
export function parseConfig(text: string): ParsedConfig {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch (err) {
    return { ok: false, error: `It isn't valid JSON (${(err as Error).message}).` };
  }
  if (!isObject(doc)) return { ok: false, error: "It isn't a JSON object." };
  const game = doc.game;
  if (game === undefined) return { ok: true, mods: [] };
  if (!isObject(game)) return { ok: false, error: "Its game setting isn't an object." };
  const mods = game.mods;
  if (mods === undefined) return { ok: true, mods: [] };
  if (!Array.isArray(mods)) return { ok: false, error: "Its game.mods isn't a list." };
  const out: ConfigMod[] = [];
  for (const m of mods) {
    if (!isObject(m) || typeof m.modId !== "string")
      return { ok: false, error: "An entry in game.mods has no modId." };
    const mod: ConfigMod = { ...m, modId: m.modId, name: typeof m.name === "string" ? m.name : "" };
    if (typeof m.version === "string" && m.version !== "") mod.version = m.version;
    else delete mod.version;
    out.push(mod);
  }
  return { ok: true, mods: out };
}

// indentOf is the indent a JSON text uses (its first indented line's),
// so a saved config looks as it did; four spaces if it has none.
export function indentOf(text: string): string | number {
  const m = /\n([ \t]+)\S/.exec(text);
  return m?.[1] ?? 4;
}

// withMods is the config's text with game.mods set to mods, everything
// else kept (in its order), in the text's own indent. It throws if the
// text isn't a config parseConfig reads.
export function withMods(text: string, mods: ConfigMod[]): string {
  const parsed = parseConfig(text);
  if (!parsed.ok) throw new Error(`The config can't be changed: ${parsed.error}`);
  const doc = JSON.parse(text) as Json;
  const game = isObject(doc.game) ? doc.game : {};
  game.mods = mods.map((m) => {
    const out: Json = { ...m, modId: m.modId, name: m.name };
    if (!m.version) delete out.version;
    return out;
  });
  doc.game = game;
  const trailing = text.endsWith("\n") ? "\n" : "";
  return JSON.stringify(doc, null, indentOf(text)) + trailing;
}

// modRef is the mod ID in a Workshop link
// (https://reforger.armaplatform.com/workshop/595F2BF2F44836FB-RHS-StatusQuo)
// or typed alone, in capitals; undefined if there's none.
export function modRef(input: string): string | undefined {
  const s = input.trim();
  const link = /armaplatform\.com\/workshop\/([0-9a-fA-F]{16})(?![0-9a-fA-F])/.exec(s);
  const id = (link?.[1] ?? s).toUpperCase();
  return idPattern.test(id) ? id : undefined;
}

// modLink is a mod's Workshop page.
export function modLink(id: string): string {
  return `https://reforger.armaplatform.com/workshop/${id}`;
}

// addMods is mods with the new ones added at the end, except ones already
// there (by ID).
export function addMods(mods: ConfigMod[], add: ConfigMod[]): ConfigMod[] {
  const out = [...mods];
  for (const m of add) {
    if (!out.some((x) => x.modId === m.modId)) out.push(m);
  }
  return out;
}

// removeMod is mods without the one with that ID.
export function removeMod(mods: ConfigMod[], id: string): ConfigMod[] {
  return mods.filter((m) => m.modId !== id);
}

// pinMod is mods with that one's version pinned, or unpinned (newest) for
// undefined or "".
export function pinMod(mods: ConfigMod[], id: string, version: string | undefined): ConfigMod[] {
  return mods.map((m) => {
    if (m.modId !== id) return m;
    const out: ConfigMod = { ...m };
    if (version) out.version = version;
    else delete out.version;
    return out;
  });
}

// sameMods reports whether two lists say the same thing.
export function sameMods(a: ConfigMod[], b: ConfigMod[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}
