// Sorting the egg library (#457).

import type { CatalogEgg } from "@/gen/raptor/panel/v1/catalog_pb";

export const eggSorts = {
  popular: "Popular",
  servers: "Most servers",
  newest: "Newest",
  name: "Name",
  copies: "Most copied",
} as const;

export type EggSort = keyof typeof eggSorts;

export function isEggSort(s: string | null | undefined): s is EggSort {
  return !!s && s in eggSorts;
}

// Well-known games first on a Panel with few servers yet: the library's
// most-hosted eggs, most popular first. Servers made soon outweigh it
// (each server counts ten, the head start at most 24).
const seed = [
  "minecraft/paper",
  "minecraft/fabric",
  "minecraft/vanilla",
  "minecraft/purpur",
  "steam/rust",
  "minecraft/neoforge",
  "minecraft/forge",
  "minecraft/velocity",
  "steam/palworld",
  "steam/valheim",
  "steam/cs2",
  "standalone/terraria",
  "minecraft/modrinth",
  "minecraft/bedrock",
  "steam/project-zomboid",
  "steam/ark-survival-ascended",
  "steam/7-days-to-die",
  "steam/satisfactory",
  "steam/enshrouded",
  "steam/gmod",
  "standalone/factorio",
  "standalone/fivem",
  "standalone/tmodloader",
  "minecraft/curseforge",
];

// libraryId is an egg's ID in Raptor's egg library, from where its file
// is ("…/library/minecraft/paper/egg-paper.json"); "" for others.
export function libraryId(e: Pick<CatalogEgg, "sourceUrl">): string {
  return /\/library\/([^/]+\/[^/]+)\//.exec(e.sourceUrl)?.[1] ?? "";
}

// popularity ranks an egg for "Popular": servers made from it (counted
// ten times, so real use wins), plus a head start for the well-known
// games, so a new Panel still opens on what people host.
export function popularity(e: Pick<CatalogEgg, "servers" | "sourceUrl">): number {
  const i = seed.indexOf(libraryId(e));
  return e.servers * 10 + (i < 0 ? 0 : seed.length - i);
}

const status = { published: 0, draft: 1, archived: 2 } as Record<string, number>;

type Sortable = Pick<
  CatalogEgg,
  "name" | "scope" | "status" | "certified" | "servers" | "copies" | "sourceUrl"
> & { updatedAt?: { seconds: bigint } };

// sortEggs orders the library: the org's own before Raptor's, published
// before drafts and archived ones, then by the chosen order, then
// certified first and by name.
export function sortEggs<T extends Sortable>(list: T[], by: EggSort = "popular"): T[] {
  const updated = (e: T) => Number(e.updatedAt?.seconds ?? 0n);
  const key: Record<EggSort, (a: T, b: T) => number> = {
    popular: (a, b) => popularity(b) - popularity(a),
    servers: (a, b) => b.servers - a.servers,
    newest: (a, b) => updated(b) - updated(a),
    name: (a, b) => a.name.localeCompare(b.name),
    copies: (a, b) => b.copies - a.copies,
  };
  return [...list].sort(
    (a, b) =>
      Number(a.scope === "official") - Number(b.scope === "official") ||
      (status[a.status] ?? 0) - (status[b.status] ?? 0) ||
      key[by](a, b) ||
      Number(b.certified) - Number(a.certified) ||
      a.name.localeCompare(b.name),
  );
}
