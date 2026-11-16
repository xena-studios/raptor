// The game settings form (#424): typed fields over a game's own config
// file, starting with Minecraft's server.properties. A field shows only
// when the file has its key: the game writes every setting its version
// knows on its first start, so the form follows the version on its own.

import { propertyValue, withProperty } from "./minecraft-text.ts";

export type Field = {
  key: string;
  label: string;
  help: string;
  section: "Gameplay" | "Players" | "World" | "Performance";
} & (
  | { type: "bool"; default: boolean }
  | { type: "number"; default: number; min: number; max: number }
  | { type: "choice"; default: string; choices: [value: string, label: string][] }
  | { type: "text"; default: string }
);

// minecraftFields are server.properties' settings people change, with
// what they do.
export const minecraftFields: Field[] = [
  {
    key: "difficulty",
    label: "Difficulty",
    section: "Gameplay",
    type: "choice",
    default: "easy",
    choices: [
      ["peaceful", "Peaceful"],
      ["easy", "Easy"],
      ["normal", "Normal"],
      ["hard", "Hard"],
    ],
    help: "How hard mobs hit, and whether hunger can kill.",
  },
  {
    key: "gamemode",
    label: "Game mode",
    section: "Gameplay",
    type: "choice",
    default: "survival",
    choices: [
      ["survival", "Survival"],
      ["creative", "Creative"],
      ["adventure", "Adventure"],
      ["spectator", "Spectator"],
    ],
    help: "The mode new players start in.",
  },
  {
    key: "force-gamemode",
    label: "Force the game mode",
    section: "Gameplay",
    type: "bool",
    default: false,
    help: "Put players back in the game mode above every time they join.",
  },
  {
    key: "hardcore",
    label: "Hardcore",
    section: "Gameplay",
    type: "bool",
    default: false,
    help: "Players who die can only spectate. Difficulty is locked to Hard.",
  },
  {
    key: "pvp",
    label: "Player versus player",
    section: "Gameplay",
    type: "bool",
    default: true,
    help: "Players can hurt each other.",
  },
  {
    key: "allow-flight",
    label: "Allow flight",
    section: "Gameplay",
    type: "bool",
    default: false,
    help: "Don't kick players for flying. Turn on if a plugin or mod lets them fly.",
  },
  {
    key: "allow-nether",
    label: "The Nether",
    section: "Gameplay",
    type: "bool",
    default: true,
    help: "Players can go to the Nether.",
  },
  {
    key: "spawn-protection",
    label: "Spawn protection",
    section: "Gameplay",
    type: "number",
    default: 16,
    min: 0,
    max: 1000,
    help: "Blocks around spawn that only operators can change. 0 turns it off.",
  },
  {
    key: "enable-command-block",
    label: "Command blocks",
    section: "Gameplay",
    type: "bool",
    default: false,
    help: "Command blocks run their commands.",
  },
  {
    key: "max-players",
    label: "Max players",
    section: "Players",
    type: "number",
    default: 20,
    min: 1,
    max: 10000,
    help: "How many can be online at once.",
  },
  {
    key: "white-list",
    label: "Whitelist",
    section: "Players",
    type: "bool",
    default: false,
    help: "Only players on the whitelist can join.",
  },
  {
    key: "enforce-whitelist",
    label: "Enforce the whitelist",
    section: "Players",
    type: "bool",
    default: false,
    help: "Kick players who are online but taken off the whitelist.",
  },
  {
    key: "online-mode",
    label: "Check accounts with Mojang",
    section: "Players",
    type: "bool",
    default: true,
    help: "Leave on unless the server is behind a proxy (Velocity, BungeeCord) that checks them. Off, anyone can join as anyone.",
  },
  {
    key: "player-idle-timeout",
    label: "Kick idle players after (minutes)",
    section: "Players",
    type: "number",
    default: 0,
    min: 0,
    max: 1440,
    help: "0 never kicks.",
  },
  {
    key: "op-permission-level",
    label: "Operator level",
    section: "Players",
    type: "choice",
    default: "4",
    choices: [
      ["1", "1: build at spawn"],
      ["2", "2: cheat commands"],
      ["3", "3: kick and ban"],
      ["4", "4: everything"],
    ],
    help: "What operators may do.",
  },
  {
    key: "hide-online-players",
    label: "Hide who's online",
    section: "Players",
    type: "bool",
    default: false,
    help: "The server list doesn't show players' names.",
  },
  {
    key: "level-name",
    label: "World folder",
    section: "World",
    type: "text",
    default: "world",
    help: "The world the server loads. A name that doesn't exist makes a new world.",
  },
  {
    key: "level-seed",
    label: "Seed",
    section: "World",
    type: "text",
    default: "",
    help: "Only used when a new world is made. Empty: random.",
  },
  {
    key: "level-type",
    label: "World type",
    section: "World",
    type: "choice",
    default: "minecraft:normal",
    choices: [
      ["minecraft:normal", "Normal"],
      ["minecraft:flat", "Flat"],
      ["minecraft:large_biomes", "Large biomes"],
      ["minecraft:amplified", "Amplified"],
      ["minecraft:single_biome_surface", "Single biome"],
    ],
    help: "Only used when a new world is made.",
  },
  {
    key: "generate-structures",
    label: "Structures",
    section: "World",
    type: "bool",
    default: true,
    help: "New chunks get villages, temples, and the like.",
  },
  {
    key: "max-world-size",
    label: "World border (blocks from the centre)",
    section: "World",
    type: "number",
    default: 29999984,
    min: 1,
    max: 29999984,
    help: "How far the world goes.",
  },
  {
    key: "view-distance",
    label: "View distance (chunks)",
    section: "Performance",
    type: "number",
    default: 10,
    min: 3,
    max: 32,
    help: "How far players see. Lower uses less memory and CPU; the biggest lever on a busy server.",
  },
  {
    key: "simulation-distance",
    label: "Simulation distance (chunks)",
    section: "Performance",
    type: "number",
    default: 10,
    min: 3,
    max: 32,
    help: "How far from players things grow, move, and tick.",
  },
  {
    key: "sync-chunk-writes",
    label: "Write chunks right away",
    section: "Performance",
    type: "bool",
    default: true,
    help: "Safer if the server crashes, slower on slow disks.",
  },
];

// minecraftManaged are keys Raptor sets from the server's settings at
// every start (the egg's config parsers): shown, not edited here.
export const minecraftManaged: Record<string, string> = {
  "server-port": "Network",
  "server-ip": "Network",
  "query.port": "Network",
};

// Kept out of the form: the MOTD has its own editor (icon and message).
const elsewhere = ["motd"];

// Value is a field's current value, as text.
export function fieldValue(text: string, f: Field): string | undefined {
  return propertyValue(text, f.key);
}

// formFields are the fields the file has, in order.
export function formFields(text: string, fields = minecraftFields): Field[] {
  return fields.filter((f) => propertyValue(text, f.key) !== undefined);
}

// otherKeys are the file's keys the form doesn't know: listed, untouched.
export function otherKeys(text: string, fields = minecraftFields): string[] {
  const known = new Set([
    ...fields.map((f) => f.key),
    ...Object.keys(minecraftManaged),
    ...elsewhere,
  ]);
  const out: string[] = [];
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*([^\s#!=:][^=:\s]*)\s*[=:]/.exec(line);
    if (m?.[1] && !known.has(m[1])) out.push(m[1]);
  }
  return out;
}

// invalid says what's wrong with a value for a field, or "" if it's fine.
export function invalid(f: Field, value: string): string {
  if (f.type === "number") {
    if (!/^-?\d+$/.test(value.trim())) return "A whole number.";
    const n = Number(value);
    if (n < f.min || n > f.max) return `Between ${f.min} and ${f.max}.`;
  }
  if (f.type === "text" && f.key === "level-name" && !/^[^/\\:*?"<>|]{1,64}$/.test(value)) {
    return 'A folder name, without / \\ : * ? " < > |.';
  }
  return "";
}

// applyValues writes changed values into the file, each in place.
export function applyValues(text: string, values: Record<string, string>): string {
  let out = text;
  for (const [k, v] of Object.entries(values)) out = withProperty(out, k, v);
  return out;
}
