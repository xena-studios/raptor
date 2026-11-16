// Raptor's egg library (../library, library/README.md), read at build time,
// so the site's catalog is always what the Panel ships: a merged egg gets
// its page with the next site build. Internal eggs (the test server) stay out.
import { readdirSync, readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { parse } from "yaml";

// The build runs in site/, and this file is bundled elsewhere first, so the
// path is from the working directory, not from here.
const root = join(process.cwd(), "..", "library");

export type Category = "minecraft" | "steam" | "standalone" | "voice" | "software" | "generic";

// The order and words the catalog uses for each folder.
export const categories: { id: Category; name: string; blurb: string }[] = [
  { id: "minecraft", name: "Minecraft", blurb: "Java and Bedrock, every server software, modpacks, and proxies." },
  { id: "steam", name: "Steam games", blurb: "Installed and kept updated with SteamCMD." },
  { id: "standalone", name: "Other games", blurb: "Games with their own server, outside Steam." },
  { id: "voice", name: "Voice", blurb: "Voice and chat servers for your community." },
  { id: "software", name: "Apps", blurb: "Bots, dashboards, and self-hosted apps." },
  { id: "generic", name: "Runtimes", blurb: "A language and your own code: Node.js, Python, Go, and more." },
];

export type Variable = { name: string; description: string; env: string; default: string };
export type Entry = {
  /** The egg's ID in the Panel: `<category>/<folder>`. Never changes. */
  id: string;
  category: Category;
  slug: string;
  name: string;
  description: string;
  arch: string[];
  /** Live player counts in the Panel (a status query the game answers). */
  players: boolean;
  /** What the server's Add-ons tab installs, in words ("plugins from Modrinth, Hangar, and CurseForge"). */
  addons: string[];
  /** A modpack picker, and where its packs come from. */
  modpack: string | null;
  /** The app has its own web page the Panel links to. */
  web: boolean;
  workshop: boolean;
  /** Raptor pauses saving around backups, so worlds are backed up whole. */
  safeBackups: boolean;
  /** The runtime images, by name ("Java 21", "SteamCMD"). */
  images: string[];
  variables: Variable[];
  credits: string[];
  source: string;
  /** Path to icon.png, relative to the library folder. */
  icon: string;
};

type Egg = {
  name: string;
  description?: string;
  docker_images?: Record<string, string>;
  variables?: { name: string; description?: string; env_variable: string; default_value?: string; user_viewable?: boolean }[];
};
type Meta = {
  internal?: boolean;
  arch?: string[];
  players?: unknown;
  addons?: Record<string, unknown>;
  modpack?: { source?: string };
  web?: unknown;
  workshop?: unknown;
  backup?: unknown;
  credits?: string[];
  source?: { repo: string; path: string };
};

// What each add-on kind in raptor.yaml installs, and from where
// (docs-site servers/addons.md).
const addonWords: Record<string, string> = {
  plugins: "plugins from Modrinth, Hangar, and CurseForge",
  mods: "mods from Modrinth and CurseForge",
  datapacks: "datapacks from Modrinth",
  umod: "Oxide and Carbon plugins from uMod",
  factorio: "mods from the Factorio mod portal",
};
const packSource: Record<string, string> = {
  modrinth: "Modrinth",
  curseforge: "CurseForge",
  ftb: "Feed The Beast",
  technic: "Technic",
};

function load(): Entry[] {
  const out: Entry[] = [];
  for (const { id: category } of categories) {
    const dir = join(root, category);
    if (!existsSync(dir)) continue;
    for (const slug of readdirSync(dir).sort()) {
      const folder = join(dir, slug);
      const eggFile = readdirSync(folder).find((f) => f.startsWith("egg-") && f.endsWith(".json"));
      if (!eggFile || !existsSync(join(folder, "raptor.yaml"))) continue;
      const egg = JSON.parse(readFileSync(join(folder, eggFile), "utf8")) as Egg;
      const meta = (parse(readFileSync(join(folder, "raptor.yaml"), "utf8")) ?? {}) as Meta;
      if (meta.internal) continue;
      const src = meta.source ?? { repo: "xena-studios/raptor", path: `library/${category}/${slug}/${eggFile}` };
      out.push({
        id: `${category}/${slug}`,
        category,
        slug,
        name: egg.name,
        description: egg.description ?? "",
        arch: meta.arch ?? ["amd64"],
        players: Boolean(meta.players),
        addons: Object.keys(meta.addons ?? {}).map((k) => addonWords[k] ?? k),
        modpack: meta.modpack ? (packSource[meta.modpack.source ?? ""] ?? "a modpack site") : null,
        web: Boolean(meta.web),
        workshop: Boolean(meta.workshop),
        safeBackups: Boolean(meta.backup),
        images: Object.keys(egg.docker_images ?? {}),
        variables: (egg.variables ?? [])
          .filter((v) => v.user_viewable !== false)
          .map((v) => ({ name: v.name, description: v.description ?? "", env: v.env_variable, default: v.default_value ?? "" })),
        credits: meta.credits ?? [],
        source: `https://github.com/${src.repo}/blob/main/${src.path}`,
        icon: `${category}/${slug}/icon.png`,
      });
    }
  }
  return out;
}

export const library = load();

export const byCategory = (c: Category) => library.filter((e) => e.category === c);

export const categoryName = (c: Category) => categories.find((x) => x.id === c)!.name;

/** The page for an entry. */
export const href = (e: Pick<Entry, "category" | "slug">) => `/library/${e.category}/${e.slug}/`;

/** "Plugins, mods, and datapacks" */
export const list = (words: string[]) =>
  words.length < 2 ? (words[0] ?? "") : `${words.slice(0, -1).join(", ")}${words.length > 2 ? "," : ""} and ${words.at(-1)}`;
