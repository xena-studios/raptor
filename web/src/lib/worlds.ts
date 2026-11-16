// A Minecraft server's worlds (#426): folders with a level.dat, the
// dimensions Paper keeps beside them (world_nether, world_the_end), and
// which one server.properties' level-name loads.

export type World = {
  // The folder, from the server's root (what level-name holds).
  path: string;
  // Paper's and Spigot's other dimensions for it, if any.
  dimensions: string[];
};

type Dir = { path: string; entries: { name: string; type: string }[] };

// findWorlds is the worlds among listed folders: those with a level.dat.
// A dimension folder (name_nether, name_the_end) next to its world is
// part of it, not a world of its own.
export function findWorlds(dirs: Dir[]): World[] {
  const worlds = dirs
    .filter((d) => d.entries.some((e) => e.name === "level.dat" && e.type === "file"))
    .map((d) => d.path)
    .sort((a, b) => a.localeCompare(b));
  const out: World[] = [];
  for (const w of worlds) {
    const base = w.replace(/_(nether|the_end)$/, "");
    if (base !== w && worlds.includes(base)) continue;
    out.push({
      path: w,
      dimensions: [`${w}_nether`, `${w}_the_end`].filter((p) => worlds.includes(p)),
    });
  }
  return out;
}

// folderName is a safe folder name for an uploaded world, from its file.
export function folderName(file: string): string {
  const base = file
    .replace(/\.(zip|tar\.gz|tgz|tar)$/i, "")
    .replace(/[^A-Za-z0-9 _.-]+/g, "_")
    .trim();
  return base.replace(/^\.+/, "").slice(0, 64) || "uploaded-world";
}

// Game rules people change, with what they do, for the ones a world has.
export const knownRules: Record<string, string> = {
  keepInventory: "Players keep their items when they die.",
  keep_inventory: "Players keep their items when they die.",
  doDaylightCycle: "The sun moves; off, the time stays as it is.",
  advance_time: "The sun moves; off, the time stays as it is.",
  doWeatherCycle: "The weather changes.",
  advance_weather: "The weather changes.",
  doMobSpawning: "Mobs spawn on their own.",
  spawn_mobs: "Mobs spawn on their own.",
  mobGriefing: "Creepers, endermen, and others can change blocks.",
  mob_griefing: "Creepers, endermen, and others can change blocks.",
  doFireTick: "Fire spreads and burns out.",
  fire_spread_radius_around_player: "How far fire spreads from players.",
  playersSleepingPercentage: "Percent of players who must sleep to skip the night.",
  players_sleeping_percentage: "Percent of players who must sleep to skip the night.",
  doInsomnia: "Phantoms spawn when players don't sleep.",
  spawn_phantoms: "Phantoms spawn when players don't sleep.",
  pvp: "Players can hurt each other.",
  announceAdvancements: "Advancements are announced in chat.",
  show_advancement_messages: "Advancements are announced in chat.",
  showDeathMessages: "Deaths are announced in chat.",
  show_death_messages: "Deaths are announced in chat.",
  naturalRegeneration: "Players heal when they're fed.",
  natural_health_regeneration: "Players heal when they're fed.",
  randomTickSpeed: "How fast crops grow and leaves decay (3 is normal).",
  random_tick_speed: "How fast crops grow and leaves decay (3 is normal).",
};

// validRule: a game rule's name, safe to put in a console command.
export function validRule(name: string): boolean {
  return /^[A-Za-z][A-Za-z0-9_:]{0,63}$/.test(name);
}

// validRuleValue: true, false, or a whole number.
export function validRuleValue(v: string): boolean {
  return /^(true|false|-?\d{1,9})$/.test(v);
}
