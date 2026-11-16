// Bedrock's behaviour and resource packs (#416): what a .mcpack or
// .mcaddon holds (each pack's manifest), and a world's lists of the packs
// it loads (world_behavior_packs.json, world_resource_packs.json).

import { entryBytes, readZip } from "./zip.ts";

export type PackKind = "behavior" | "resource";

export type Pack = {
  name: string;
  uuid: string;
  version: number[];
  kind: PackKind;
  // Where its manifest.json is in its archive ("" for the top).
  root: string;
  // Script modules need the world's Beta APIs experiment.
  experimental: boolean;
  // The archive it's in: the upload itself, or a .mcpack inside it.
  archive: Uint8Array;
};

// manifestOf reads a pack's manifest.json (comments allowed, as Bedrock
// allows them); undefined when it isn't a pack's.
export function manifestOf(text: string): Omit<Pack, "root" | "archive"> | undefined {
  try {
    const m = JSON.parse(text.replace(/^\s*\/\/.*$/gm, "")) as {
      header?: { name?: string; uuid?: string; version?: unknown };
      modules?: { type?: string }[];
    };
    const uuid = m.header?.uuid;
    const version = m.header?.version;
    if (!uuid || !/^[0-9a-fA-F-]{36}$/.test(uuid) || !Array.isArray(version)) return undefined;
    const types = (m.modules ?? []).map((x) => x.type ?? "");
    const kind: PackKind | undefined = types.includes("resources")
      ? "resource"
      : types.some((t) => t === "data" || t === "script" || t === "javascript")
        ? "behavior"
        : undefined;
    if (!kind) return undefined;
    return {
      name: (m.header?.name ?? "").replace(/§./g, "") || uuid,
      uuid: uuid.toLowerCase(),
      version: version.map(Number),
      kind,
      experimental: types.some((t) => t === "script" || t === "javascript"),
    };
  } catch {
    return undefined;
  }
}

// packsIn are the packs in an archive: manifests in it, and in .mcpack
// files inside it (a .mcaddon), one level down.
export async function packsIn(archive: Uint8Array, depth = 0): Promise<Pack[]> {
  const entries = readZip(archive);
  const out: Pack[] = [];
  for (const e of entries) {
    if (/(^|\/)manifest\.json$/i.test(e.name)) {
      const m = manifestOf(new TextDecoder().decode(await entryBytes(e)));
      if (m)
        out.push({
          ...m,
          root: e.name.replace(/manifest\.json$/i, "").replace(/\/$/, ""),
          archive,
        });
    } else if (/\.mcpack$/i.test(e.name) && depth === 0) {
      out.push(...(await packsIn(await entryBytes(e), 1)));
    }
  }
  return out;
}

export type WorldPack = { pack_id: string; version: number[] };

export function parseWorldPacks(text: string | undefined): WorldPack[] {
  if (!text?.trim()) return [];
  try {
    const v = JSON.parse(text);
    return Array.isArray(v) ? v.filter((x) => typeof x?.pack_id === "string") : [];
  } catch {
    return [];
  }
}

// withPack is a world's list with a pack on it (replacing an older version
// of the same pack), as Bedrock writes it.
export function withPack(list: WorldPack[], p: { uuid: string; version: number[] }): string {
  const out = list.filter((x) => x.pack_id.toLowerCase() !== p.uuid);
  out.push({ pack_id: p.uuid, version: p.version });
  return `${JSON.stringify(out, null, 2)}\n`;
}

export function withoutPack(list: WorldPack[], uuid: string): string {
  return `${JSON.stringify(
    list.filter((x) => x.pack_id.toLowerCase() !== uuid.toLowerCase()),
    null,
    2,
  )}\n`;
}

// packFolder is a safe folder name for a pack.
export function packFolder(name: string, uuid: string): string {
  const base = name
    .replace(/[^A-Za-z0-9 _.-]+/g, "_")
    .replace(/^[.\s]+/, "")
    .trim()
    .slice(0, 48);
  return `${base || "pack"}-${uuid.slice(0, 8)}`;
}

export const listFile: Record<PackKind, string> = {
  behavior: "world_behavior_packs.json",
  resource: "world_resource_packs.json",
};
export const packDir: Record<PackKind, string> = {
  behavior: "behavior_packs",
  resource: "resource_packs",
};
