// Add-ons as code (#437, docs/EGGS.md#add-on-lists): a server's add-ons
// as a small JSON file, to export, check in, and apply to another server.
// Each add-on is named by its source, project, and version, never by a
// URL: applying a list asks the Panel for the file again, and Wings checks
// its hash as on any install. Files no source knows (added by hand) are
// listed by name and hash only. No imports, so it's tested on its own.

// The format's version. A list with a higher one was made by a newer
// Raptor and is refused rather than half read.
export const listFormat = 1;

// LockedAddon is one add-on in a list.
export type LockedAddon = {
  // "modrinth", "hangar", "curseforge", "umod", "factorio", …
  source: string;
  // The source's project ID (Modrinth's ID, Hangar's slug, the Factorio
  // mod's name, …).
  project: string;
  // The source's version ID (Factorio's is "mod@version"; uMod's is its
  // version number, and uMod only has its newest).
  version_id: string;
  // For people reading the file; applying ignores them.
  name?: string;
  version_number?: string;
  file?: string;
  // The file's hashes as the source lists them. Applying refuses a file
  // whose hash changed at the source since the list was made.
  sha512?: string;
  sha256?: string;
  sha1?: string;
};

// HandFile is a file no source knows: copy it yourself.
export type HandFile = { file: string; sha512: string };

// KindList is one add-on kind's part of a list (plugins, mods, …).
export type KindList = {
  // What the server it came from runs, to warn when they differ.
  loaders: string[];
  game_version?: string;
  addons: LockedAddon[];
  by_hand: HandFile[];
};

export type AddonList = {
  version: number;
  // By add-on kind, as in the egg's addons.
  kinds: Record<string, KindList>;
};

// Installed is one file in the server's add-on folder, with what it was
// identified as, if anything.
export type Installed = {
  file: string;
  sha512?: string;
  addon?: Omit<LockedAddon, "file" | "sha512"> & { sha512?: string };
};

const key = (a: { source: string; project: string }) => `${a.source}:${a.project}`;

// exportList is a server's add-ons of one kind as a list, sorted so the
// same add-ons give the same file.
export function exportList(
  kind: string,
  loaders: string[],
  gameVersion: string,
  installed: Installed[],
): AddonList {
  const seen = new Set<string>();
  const addons: LockedAddon[] = [];
  const byHand: HandFile[] = [];
  for (const f of installed) {
    if (f.addon) {
      if (seen.has(key(f.addon))) continue;
      seen.add(key(f.addon));
      addons.push(
        clean({
          source: f.addon.source,
          project: f.addon.project,
          version_id: f.addon.version_id,
          name: f.addon.name,
          version_number: f.addon.version_number,
          file: f.file,
          sha512: f.addon.sha512 || f.sha512,
          sha256: f.addon.sha256,
          sha1: f.addon.sha1,
        }),
      );
    } else if (f.sha512) {
      byHand.push({ file: f.file, sha512: f.sha512 });
    }
  }
  const by = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  addons.sort((a, b) =>
    by((a.name ?? a.project).toLowerCase(), (b.name ?? b.project).toLowerCase()),
  );
  byHand.sort((a, b) => by(a.file, b.file));
  const list: KindList = { loaders, addons, by_hand: byHand };
  if (gameVersion) list.game_version = gameVersion;
  return { version: listFormat, kinds: { [kind]: list } };
}

function clean(a: LockedAddon): LockedAddon {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(a)) if (v) out[k] = v;
  return out as LockedAddon;
}

// stringifyList is the file's text.
export function stringifyList(list: AddonList): string {
  return `${JSON.stringify(list, null, 2)}\n`;
}

// listFileName is what an exported list is saved as.
export function listFileName(server: string, kind: string): string {
  const slug = server
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  return `${slug || "server"}-${kind}.raptor-addons.json`;
}

const maxAddons = 1000;
const ident = /^[A-Za-z0-9._@:+-]{1,200}$/;
const sourceRe = /^[a-z0-9-]{1,32}$/;
const hashes: Record<"sha512" | "sha256" | "sha1", number> = { sha512: 128, sha256: 64, sha1: 40 };
const hex = (s: string, n: number) => s.length === n && /^[0-9a-f]+$/.test(s);
// A file name: no folders, nothing hidden.
const fileRe = /^[^/\\\0]{1,255}$/;

export class ListError extends Error {}

// parseList reads a list, or throws a ListError saying what's wrong.
export function parseList(text: string): AddonList {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new ListError("That isn't an add-on list: it isn't JSON.");
  }
  if (!isObject(raw) || !("version" in raw) || !isObject(raw.kinds)) {
    throw new ListError("That isn't an add-on list: it has no version and kinds.");
  }
  if (typeof raw.version !== "number" || !Number.isInteger(raw.version) || raw.version < 1) {
    throw new ListError("The list's version isn't a number.");
  }
  if (raw.version > listFormat) {
    throw new ListError(
      `The list is version ${raw.version}, from a newer Raptor. This one reads version ${listFormat}.`,
    );
  }
  const kinds: Record<string, KindList> = {};
  for (const [kind, v] of Object.entries(raw.kinds)) {
    if (!/^[a-z0-9-]{1,32}$/.test(kind)) throw new ListError(`"${kind}" isn't an add-on kind.`);
    kinds[kind] = parseKind(kind, v);
  }
  return { version: raw.version, kinds };
}

function parseKind(kind: string, v: unknown): KindList {
  const at = (s: string) => new ListError(`${kind}: ${s}`);
  if (!isObject(v)) throw at("isn't an object.");
  const loaders = v.loaders ?? [];
  if (!Array.isArray(loaders) || !loaders.every((l) => typeof l === "string" && ident.test(l))) {
    throw at("loaders isn't a list of names.");
  }
  if (v.game_version !== undefined && typeof v.game_version !== "string") {
    throw at("game_version isn't text.");
  }
  const addons = v.addons ?? [];
  const byHand = v.by_hand ?? [];
  if (!Array.isArray(addons)) throw at("addons isn't a list.");
  if (!Array.isArray(byHand)) throw at("by_hand isn't a list.");
  if (addons.length + byHand.length > maxAddons) throw at(`more than ${maxAddons} files.`);
  const seen = new Set<string>();
  const out: KindList = { loaders, addons: [], by_hand: [] };
  if (v.game_version) out.game_version = v.game_version;
  addons.forEach((a, i) => {
    const n = `add-on ${i + 1}`;
    if (!isObject(a)) throw at(`${n} isn't an object.`);
    const { source, project, version_id } = a;
    if (typeof source !== "string" || !sourceRe.test(source)) throw at(`${n} has no source.`);
    if (typeof project !== "string" || !ident.test(project)) throw at(`${n} has no project.`);
    if (typeof version_id !== "string" || !ident.test(version_id)) {
      throw at(`${n} (${project}) has no version_id.`);
    }
    if (seen.has(key({ source, project }))) throw at(`${project} is listed twice.`);
    seen.add(key({ source, project }));
    const e: LockedAddon = { source, project, version_id };
    for (const f of ["name", "version_number"] as const) {
      if (a[f] === undefined) continue;
      if (typeof a[f] !== "string" || a[f].length > 200) throw at(`${n}'s ${f} isn't text.`);
      e[f] = a[f];
    }
    if (a.file !== undefined) {
      if (typeof a.file !== "string" || !fileRe.test(a.file)) throw at(`${n}'s file isn't a name.`);
      e.file = a.file;
    }
    for (const [h, len] of Object.entries(hashes) as [keyof typeof hashes, number][]) {
      if (a[h] === undefined) continue;
      if (typeof a[h] !== "string" || !hex(a[h], len)) throw at(`${n}'s ${h} isn't a ${h} hash.`);
      e[h] = a[h];
    }
    out.addons.push(e);
  });
  byHand.forEach((f, i) => {
    if (!isObject(f)) throw at(`by_hand ${i + 1} isn't an object.`);
    if (typeof f.file !== "string" || !fileRe.test(f.file))
      throw at(`by_hand ${i + 1} has no file.`);
    if (typeof f.sha512 !== "string" || !hex(f.sha512, 128)) {
      throw at(`by_hand ${i + 1} (${f.file}) has no sha512.`);
    }
    out.by_hand.push({ file: f.file, sha512: f.sha512 });
  });
  return out;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// kindOf is the part of a list for a kind, or throws a ListError naming
// what the list has instead.
export function kindOf(list: AddonList, kind: string, noun = kind): KindList {
  const k = list.kinds[kind];
  if (k) return k;
  const has = Object.keys(list.kinds);
  throw new ListError(
    has.length === 0
      ? "The list is empty."
      : `The list has no ${noun}; it has ${has.join(", ")}. Import it on that tab.`,
  );
}

// ListDiff is what applying a list to a server changes.
export type ListDiff = {
  // Not on the server.
  install: LockedAddon[];
  // On the server at another version (newer or older).
  update: { want: LockedAddon; have: Installed }[];
  // Identified on the server but not in the list (and second copies).
  remove: Installed[];
  // Already there at the listed version.
  same: LockedAddon[];
  // Hand-added files in the list that aren't on the server: copy them
  // yourself.
  copy: HandFile[];
  // Hand-added files on the server that aren't in the list: kept, since
  // nothing says what they are.
  kept: Installed[];
};

// diffList compares a list with what a server has. Add-ons match by
// source and project, hand-added files by SHA-512.
export function diffList(want: KindList, have: Installed[]): ListDiff {
  const out: ListDiff = { install: [], update: [], remove: [], same: [], copy: [], kept: [] };
  const onServer = new Map<string, Installed>();
  const handHashes = new Set<string>();
  for (const f of have) {
    if (f.addon) {
      if (onServer.has(key(f.addon))) out.remove.push(f);
      else onServer.set(key(f.addon), f);
    } else if (f.sha512) {
      handHashes.add(f.sha512);
    }
  }
  const listed = new Set<string>();
  for (const a of want.addons) {
    listed.add(key(a));
    const h = onServer.get(key(a));
    if (!h) out.install.push(a);
    else if (h.addon?.version_id === a.version_id) out.same.push(a);
    else out.update.push({ want: a, have: h });
  }
  for (const [k, f] of onServer) if (!listed.has(k)) out.remove.push(f);
  const wantHand = new Set(want.by_hand.map((f) => f.sha512));
  for (const f of want.by_hand) if (!handHashes.has(f.sha512)) out.copy.push(f);
  for (const f of have) if (!f.addon && f.sha512 && !wantHand.has(f.sha512)) out.kept.push(f);
  return out;
}

// changes is how many installs, updates, and removals a diff makes.
export function changes(d: ListDiff, removing = true): number {
  return d.install.length + d.update.length + (removing ? d.remove.length : 0);
}

// SourceFile is a version's file as the Panel lists it.
export type SourceFile = {
  url: string;
  filename: string;
  sha512: string;
  sha256: string;
  sha1: string;
  manualUrl: string;
};

export type Pick =
  | { ok: true; versionId: string; versionNumber: string; file: SourceFile; newest: boolean }
  | { ok: false; problem: string };

// pickVersion finds a listed add-on's version among the versions the
// Panel lists for the server (newest first). With newest, an add-on whose
// version isn't there gets the newest one instead. A file whose hash
// changed at the source since the list was made is refused.
export function pickVersion(
  want: LockedAddon,
  versions: { id: string; versionNumber: string; file?: SourceFile }[],
  newest: boolean,
): Pick {
  const exact = versions.find((v) => v.id === want.version_id);
  const v = exact ?? (newest ? versions.find((x) => x.file) : undefined);
  if (!v) {
    return {
      ok: false,
      problem: versions.length
        ? `${want.version_number ?? want.version_id} isn't listed for this server's version`
        : "no version runs on this server",
    };
  }
  if (!v.file) return { ok: false, problem: "it has no file to install" };
  if (exact) {
    for (const h of ["sha512", "sha256", "sha1"] as const) {
      const was = want[h];
      if (was && v.file[h] && v.file[h] !== was) {
        return { ok: false, problem: "its file changed at the source since the list was made" };
      }
    }
  }
  if (!v.file.url) {
    return {
      ok: false,
      problem: v.file.manualUrl
        ? "its author only allows downloads from CurseForge itself"
        : "it has no file to install",
    };
  }
  return {
    ok: true,
    versionId: v.id,
    versionNumber: v.versionNumber,
    file: v.file,
    newest: !exact,
  };
}
