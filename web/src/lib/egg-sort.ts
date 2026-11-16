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

// popularity ranks an egg for "Popular": the Panel's score (servers made
// from it, more for running ones and recent ones; 10 a server before it
// sent one), plus a head start for the well-known games, so a new Panel
// still opens on what people host.
export function popularity(
  e: Pick<CatalogEgg, "servers" | "sourceUrl"> & { popularity?: number },
): number {
  const i = seed.indexOf(libraryId(e));
  return (e.popularity || e.servers * 10) + (i < 0 ? 0 : seed.length - i);
}

// Badges (#457): the few most popular eggs of each category, by real use
// (at least 3 servers), and those most picked lately that aren't already.
export type Rank = "popular" | "trending";

export function ranks(
  list: (Pick<CatalogEgg, "id" | "category" | "servers"> & {
    popularity?: number;
    serversRecent?: number;
  })[],
  top = 3,
): Map<string, Rank> {
  const out = new Map<string, Rank>();
  const cats = new Set(list.map((e) => e.category));
  for (const c of cats) {
    const inCat = list.filter((e) => e.category === c);
    const used = inCat
      .filter((e) => e.servers >= 3)
      .sort((a, b) => (b.popularity || b.servers * 10) - (a.popularity || a.servers * 10));
    for (const e of used.slice(0, top)) out.set(e.id, "popular");
    const lately = inCat
      .filter((e) => (e.serversRecent ?? 0) >= 3 && !out.has(e.id))
      .sort((a, b) => (b.serversRecent ?? 0) - (a.serversRecent ?? 0));
    for (const e of lately.slice(0, top)) out.set(e.id, "trending");
  }
  return out;
}

const status = { published: 0, draft: 1, archived: 2 } as Record<string, number>;

type Sortable = Pick<
  CatalogEgg,
  "name" | "scope" | "status" | "certified" | "servers" | "copies" | "sourceUrl"
> & { updatedAt?: { seconds: bigint }; popularity?: number };

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
