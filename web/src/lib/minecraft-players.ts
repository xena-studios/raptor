// Minecraft's player lists (#422): whitelist.json, ops.json,
// banned-players.json, banned-ips.json, and usercache.json (players seen).
// A running server changes them by console command (it would overwrite an
// edited file); a stopped one by editing the files, as the game writes them.

export type Player = { uuid: string; name: string };
export type Op = Player & { level: number; bypassesPlayerLimit: boolean };
export type Ban = Player & { created: string; source: string; expires: string; reason: string };
export type IpBan = {
  ip: string;
  created: string;
  source: string;
  expires: string;
  reason: string;
};
export type Seen = Player & { expiresOn?: string };

export const listFiles = {
  whitelist: "whitelist.json",
  ops: "ops.json",
  bans: "banned-players.json",
  ipBans: "banned-ips.json",
  seen: "usercache.json",
} as const;

// parseList is a list file's entries; [] for a file that isn't there yet
// or isn't a list.
export function parseList<T>(text: string | undefined): T[] {
  if (!text?.trim()) return [];
  try {
    const v = JSON.parse(text);
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}

// A Minecraft name: what the game accepts, and all a console command may
// carry from a user.
export function validName(s: string): boolean {
  return /^[A-Za-z0-9_]{1,16}$/.test(s);
}

export function validIp(s: string): boolean {
  const v4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;
  return v4.test(s) || (/^[0-9a-fA-F:]+$/.test(s) && s.includes(":") && s.length <= 39);
}

// reasonText is a ban reason as one plain line.
export function reasonText(s: string): string {
  return (
    s
      // biome-ignore lint/suspicious/noControlCharactersInRegex: taking them out
      .replace(/[\r\n\u0000-\u001f]+/g, " ")
      .trim()
      .slice(0, 100)
  );
}

// commands are what a running server is told; arguments are checked first.
export const commands = {
  whitelistAdd: (n: string) => `whitelist add ${n}`,
  whitelistRemove: (n: string) => `whitelist remove ${n}`,
  op: (n: string) => `op ${n}`,
  deop: (n: string) => `deop ${n}`,
  ban: (n: string, reason = "") => `ban ${n}${reasonText(reason) ? ` ${reasonText(reason)}` : ""}`,
  pardon: (n: string) => `pardon ${n}`,
  banIp: (ip: string, reason = "") =>
    `ban-ip ${ip}${reasonText(reason) ? ` ${reasonText(reason)}` : ""}`,
  pardonIp: (ip: string) => `pardon-ip ${ip}`,
  kick: (n: string) => `kick ${n}`,
};

// mcDate is a time as Minecraft writes it in ban lists.
export function mcDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())} +0000`;
}

// withEntry is a list with an entry added, or replaced where its key
// (uuid, or ip) is already there, written as Minecraft does.
export function withEntry<T extends object>(list: T[], entry: T, key: keyof T): string {
  const out = list.filter((e) => String(e[key]).toLowerCase() !== String(entry[key]).toLowerCase());
  out.push(entry);
  return `${JSON.stringify(out, null, 2)}\n`;
}

// without is a list without the entries whose name (or ip) matches.
export function without<T extends object>(list: T[], key: keyof T, value: string): string {
  const out = list.filter((e) => String(e[key]).toLowerCase() !== value.toLowerCase());
  return `${JSON.stringify(out, null, 2)}\n`;
}
