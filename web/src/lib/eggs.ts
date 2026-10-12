import { create } from "@bufbuild/protobuf";

import type { CatalogEgg, EggVariable } from "@/gen/raptor/panel/v1/catalog_pb";
import { type EggSpec, EggSpecSchema } from "@/gen/raptor/panel/v1/egg_pb";

export * from "./egg-rules";

// The egg library's words and rules, shared by its pages
// (docs/PANEL.md#eggs).

export const categories = [
  "minecraft",
  "steam",
  "standalone",
  "software",
  "database",
  "voice",
  "generic",
  "other",
] as const;

const categoryNames: Record<string, string> = {
  minecraft: "Minecraft",
  steam: "Steam",
  standalone: "Games",
  software: "Software",
  database: "Databases",
  voice: "Voice",
  generic: "Apps & bots",
  other: "Other",
};

export function categoryName(c: string): string {
  return categoryNames[c] ?? "Other";
}

export const originNames: Record<string, string> = {
  catalog: "Raptor's catalog",
  scratch: "From scratch",
  import: "Imported",
  fork: "Customized",
};

export const statusNames: Record<string, string> = {
  draft: "Draft",
  published: "Published",
  archived: "Archived",
};

// official reports whether an egg is Raptor's.
function official(e: CatalogEgg): boolean {
  return e.scope === "official";
}

export function usage(n: number): string {
  if (n === 0) return "Unused";
  return n === 1 ? "1 server" : `${n} servers`;
}

// sortEggs puts the org's own first, then Raptor's, certified first, then by
// name; published before drafts, archived last.
export function sortEggs(list: CatalogEgg[]): CatalogEgg[] {
  const rank = { published: 0, draft: 1, archived: 2 } as Record<string, number>;
  return [...list].sort(
    (a, b) =>
      Number(official(a)) - Number(official(b)) ||
      (rank[a.status] ?? 0) - (rank[b.status] ?? 0) ||
      Number(b.certified) - Number(a.certified) ||
      a.name.localeCompare(b.name),
  );
}

// ─── Add-ons ────────────────────────────────────────────────────────────────
// What an egg turns on for its servers. Each is a switch in the editor's
// Add-ons tab and a line on the egg's page.

export const addons = {
  eula: {
    label: "EULA helper",
    description:
      "Asks for the Minecraft EULA when a server is made, and accepts it for the server.",
  },
  players: {
    label: "Player count",
    description: "Shows who's online, asking the game with its query protocol.",
  },
} as const;

// addonKinds are the software add-ons an egg can turn on
// (docs/EGGS.md#add-ons): each is a page on servers made from it.
export const addonKinds: Record<
  string,
  {
    label: string;
    tab: string;
    noun: string;
    description: string;
    loaders: string[];
    dflt: string[];
  }
> = {
  plugins: {
    label: "Plugin installer",
    tab: "Plugins",
    noun: "plugin",
    description:
      "Find, install, and update plugins from Modrinth, for Paper, Spigot, Velocity, and more.",
    loaders: [
      "paper",
      "spigot",
      "bukkit",
      "purpur",
      "folia",
      "velocity",
      "bungeecord",
      "waterfall",
      "sponge",
    ],
    dflt: ["paper", "spigot", "bukkit"],
  },
  mods: {
    label: "Mod manager",
    tab: "Mods",
    noun: "mod",
    description:
      "Find, install, and update mods from Modrinth, for Fabric, Forge, NeoForge, and Quilt.",
    loaders: ["fabric", "forge", "neoforge", "quilt"],
    dflt: ["fabric"],
  },
};

// hangarLoaders are the loaders whose plugins Hangar has (Paper's, and
// Velocity's and Waterfall's).
const hangarLoaders = [
  "paper",
  "spigot",
  "bukkit",
  "purpur",
  "folia",
  "velocity",
  "bungeecord",
  "waterfall",
];

// onHangar reports whether Hangar has plugins for an add-on.
export function onHangar(kind: string, loaders: string[]): boolean {
  return kind === "plugins" && loaders.some((l) => hangarLoaders.includes(l));
}

export const sourceNames: Record<string, string> = { modrinth: "Modrinth", hangar: "Hangar" };

const loaderNames: Record<string, string> = {
  bungeecord: "BungeeCord",
  neoforge: "NeoForge",
};

export function loaderName(l: string): string {
  return loaderNames[l] ?? l.charAt(0).toUpperCase() + l.slice(1);
}

// knownFeatures are the egg features Raptor acts on; others are kept as
// they are and shown as their names.
export const knownFeatures = ["eula"];

// ─── Variables ──────────────────────────────────────────────────────────────
// Authors pick a kind and who can see it; the egg keeps Laravel rules,
// which Wings checks. The rules are shown too, and anything the kinds
// don't cover stays as written.

export type Access = "editable" | "read-only" | "hidden";

export const accessNames: Record<Access, { label: string; hint: string }> = {
  editable: { label: "Editable", hint: "Members with access to the server can change it" },
  "read-only": { label: "Read-only", hint: "Shown, but only admins change it" },
  hidden: { label: "Hidden", hint: "Only admins see and change it" },
};

export function access(v: EggVariable): Access {
  if (v.userEditable && v.userViewable) return "editable";
  return v.userViewable ? "read-only" : "hidden";
}

export function withAccess(v: EggVariable, a: Access): EggVariable {
  return { ...v, userViewable: a !== "hidden", userEditable: a === "editable" };
}

// ─── The editor ─────────────────────────────────────────────────────────────

export type EditorState = {
  spec: EggSpec;
  summary: string;
  category: string;
};

export function blankState(): EditorState {
  return {
    spec: create(EggSpecSchema, {
      startup: [{ name: "Default", command: "" }],
      stopType: "command",
      stopValue: "stop",
      installContainer: "ghcr.io/pterodactyl/installers:debian",
      installEntrypoint: "bash",
    }),
    summary: "",
    category: "other",
  };
}

// blockers is what stops an egg being saved, in the editor's words.
export function blockers(s: EggSpec): string[] {
  const out: string[] = [];
  if (!s.name.trim()) out.push("Give the egg a name.");
  if (!s.images.some((i) => i.ref.trim())) out.push("Add a runtime.");
  if (!s.startup.some((c) => c.command.trim())) out.push("Set a startup command.");
  return out;
}

export const parsers = [
  ["properties", "Properties"],
  ["yaml", "YAML"],
  ["json", "JSON"],
  ["ini", "INI"],
  ["xml", "XML"],
  ["file", "Plain text (line starts)"],
] as const;

export const signals = ["SIGINT", "SIGTERM", "SIGABRT", "SIGKILL"] as const;

// download saves an egg's file.
export function download(name: string, file: Uint8Array) {
  const text = new TextDecoder().decode(file);
  const json = text.trimStart().startsWith("{");
  const blob = new Blob([file.slice()], { type: json ? "application/json" : "application/yaml" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `egg-${
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "egg"
  }.${json ? "json" : "yaml"}`;
  a.click();
  URL.revokeObjectURL(url);
}
