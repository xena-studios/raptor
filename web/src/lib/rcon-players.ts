// Players over RCON (#423): who's on a Steam game, and checked kick, ban,
// unban, and message. Wings builds the commands (internal/wings/rcon);
// these checks match its own, so a bad ID is caught before it's sent. No
// imports, so it's tested on its own.

// The command sets (the egg's x-raptor.rcon.players).
export type RconGame = "source" | "ark" | "palworld" | "rust";

// A player as players.online lists them.
export type RconPlayer = {
  id: string;
  name: string;
  ping?: number;
  seconds?: number;
  bot?: boolean;
};

export function isRconGame(g: string): g is RconGame {
  return g === "source" || g === "ark" || g === "palworld" || g === "rust";
}

const sourceUserId = /^\d{1,5}$/;
const sourceSteamId = /^(STEAM_[0-5]:[01]:\d{1,10}|\[U:1:\d{1,10}\])$/;
const steamId64 = /^\d{17}$/;
const arkId = /^(\d{17}|[0-9a-fA-F]{32})$/;
const palworldId = /^[A-Za-z0-9_]{1,64}$/;

// validId reports whether id is one of the game's player IDs; lasting
// (for unban) leaves out a Source game's per-connection user IDs.
export function validId(game: RconGame, id: string, lasting = false): boolean {
  switch (game) {
    case "source":
      return sourceSteamId.test(id) || (!lasting && sourceUserId.test(id));
    case "ark":
      return arkId.test(id);
    case "palworld":
      return palworldId.test(id);
    case "rust":
      return steamId64.test(id);
  }
}

// What an unban takes, for the field's hint.
export const idHint: Record<RconGame, string> = {
  source: "Steam ID, like STEAM_1:0:123 or [U:1:123]",
  ark: "Steam or Epic ID",
  palworld: "Player ID, like steam_76561198…",
  rust: "SteamID64, like 76561198…",
};

// textLine is a reason or message as Wings sends it: one plain line of at
// most n characters, without quotes, backslashes, or semicolons.
export function textLine(s: string, n: number): string {
  let out = "";
  for (const ch of s) {
    const c = ch.codePointAt(0) ?? 0;
    if (c < 0x20 || c === 0x7f) out += " ";
    else if (ch !== '"' && ch !== "\\" && ch !== ";") out += ch;
  }
  out = out.split(/\s+/).filter(Boolean).join(" ");
  return Array.from(out).slice(0, n).join("").trim();
}

// Whether the game takes a reason with a kick (the others kick silently).
export function kickTakesReason(game: RconGame): boolean {
  return game === "source" || game === "rust";
}

// connectedFor is how long someone has been on, short: "5m", "1h 02m".
export function connectedFor(seconds?: number): string {
  if (!seconds || seconds < 0) return "";
  const m = Math.floor(seconds / 60);
  if (m < 1) return `${seconds}s`;
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}
