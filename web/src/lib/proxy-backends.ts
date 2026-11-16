// A Velocity proxy's backends from the org's servers (#427): its
// velocity.toml [servers] list and try order, modern forwarding, and each
// Paper backend set to take players only from it (its secret).

export type Backend = { name: string; address: string };

// readBackends is velocity.toml's [servers] and its try order.
export function readBackends(toml: string): { servers: Backend[]; try: string[] } {
  const sec = section(toml, "servers");
  const servers: Backend[] = [];
  let order: string[] = [];
  if (!sec) return { servers, try: order };
  const body = toml.slice(sec.start, sec.end);
  for (const m of body.matchAll(/^\s*("?)([A-Za-z0-9_-]+)\1\s*=\s*"([^"]*)"\s*$/gm)) {
    if (m[2] && m[3] !== undefined) servers.push({ name: m[2], address: m[3] });
  }
  const t = /^\s*try\s*=\s*\[([\s\S]*?)\]/m.exec(body);
  if (t?.[1]) order = [...t[1].matchAll(/"([^"]*)"/g)].map((x) => x[1] ?? "");
  return { servers, try: order };
}

// section is a TOML table's body: after its [name] line, up to the next
// table.
function section(toml: string, name: string): { start: number; end: number } | undefined {
  const m = new RegExp(`^\\[${name}\\][ \\t]*\\r?\\n`, "m").exec(toml);
  if (!m) return undefined;
  const start = m.index + m[0].length;
  const next = /^\[/m.exec(toml.slice(start));
  return { start, end: next ? start + next.index : toml.length };
}

const nameRe = /^[A-Za-z0-9_-]{1,32}$/;
export const validBackendName = (s: string) => nameRe.test(s);

// writeBackends is velocity.toml with its [servers] table replaced by
// these backends (tried in this order), and modern forwarding on.
export function writeBackends(toml: string, servers: Backend[], order: string[]): string {
  const body = [
    ...servers.map((s) => `${s.name} = "${s.address}"`),
    `try = [${order.map((n) => `"${n}"`).join(", ")}]`,
    "",
  ].join("\n");
  const sec = section(toml, "servers");
  let out = sec
    ? `${toml.slice(0, sec.start)}${body}\n${toml.slice(sec.end)}`
    : `${toml.replace(/\s*$/, "\n")}\n[servers]\n${body}\n`;
  if (/^\s*player-info-forwarding-mode\s*=.*$/m.test(out)) {
    out = out.replace(/^(\s*player-info-forwarding-mode\s*=\s*).*$/m, '$1"modern"');
  } else {
    out = `player-info-forwarding-mode = "modern"\n${out}`;
  }
  return out;
}

// paperForVelocity is a backend's config/paper-global.yml set to take
// players from Velocity with its secret (proxies.velocity: enabled,
// online-mode, secret), or undefined when the file isn't laid out as
// Paper writes it.
export function paperForVelocity(yml: string, secret: string): string | undefined {
  const lines = yml.split("\n");
  const proxies = lines.findIndex((l) => /^proxies:\s*$/.test(l));
  if (proxies < 0) return undefined;
  let i = proxies + 1;
  let velocity = -1;
  for (; i < lines.length && /^(\s+|$)/.test(lines[i] ?? ""); i++) {
    if (/^ {2}velocity:\s*$/.test(lines[i] ?? "")) velocity = i;
  }
  if (velocity < 0) return undefined;
  const set = (key: string, value: string) => {
    for (let j = velocity + 1; j < lines.length && /^ {4}/.test(lines[j] ?? ""); j++) {
      if (new RegExp(`^ {4}${key}:`).test(lines[j] ?? "")) {
        lines[j] = `    ${key}: ${value}`;
        return true;
      }
    }
    lines.splice(velocity + 1, 0, `    ${key}: ${value}`);
    return true;
  };
  set("enabled", "true");
  set("online-mode", "true");
  set("secret", `'${secret.replace(/'/g, "''")}'`);
  return lines.join("\n");
}

// BungeeCord and Waterfall (#427): config.yml's servers: map and the
// first listener's priorities (its try order), with ip_forward on, and
// each backend's spigot.yml settings.bungeecord on. Their forwarding has
// no secret, so a backend must not be reachable except through the proxy.

// topBlock is a top-level YAML key's block: its line, up to the next
// top-level line.
function topBlock(lines: string[], key: string): { start: number; end: number } | undefined {
  const start = lines.findIndex((l) => new RegExp(`^${key}:`).test(l));
  if (start < 0) return undefined;
  let end = start + 1;
  while (end < lines.length && /^(\s|#|-|$)/.test(lines[end] ?? "")) end++;
  // Trailing blank lines belong to what comes next.
  while (end > start + 1 && (lines[end - 1] ?? "").trim() === "") end--;
  return { start, end };
}

const unquote = (s: string) => s.trim().replace(/^(['"])(.*)\1$/, "$2");

// priorities finds the first listener's priorities: list: the key's line,
// and the list's lines after it.
function priorities(
  lines: string[],
): { key: number; start: number; end: number; indent: string } | undefined {
  const block = topBlock(lines, "listeners");
  if (!block) return undefined;
  for (let i = block.start + 1; i < block.end; i++) {
    const m = /^(\s*-?\s*)priorities:\s*$/.exec(lines[i] ?? "");
    if (!m) continue;
    // The key's own indent (after a "- " that starts the listener).
    const keyIndent = (m[1] ?? "").replace("-", " ").length;
    let end = i + 1;
    let indent = " ".repeat(keyIndent);
    for (; end < block.end; end++) {
      const item = /^(\s*)-\s+\S/.exec(lines[end] ?? "");
      if (!item || (item[1] ?? "").length < keyIndent) break;
      indent = item[1] ?? indent;
    }
    return { key: i, start: i + 1, end, indent };
  }
  return undefined;
}

// readBungeeBackends is config.yml's servers and the first listener's
// priorities.
export function readBungeeBackends(yml: string): { servers: Backend[]; try: string[] } {
  const lines = yml.split("\n");
  const servers: Backend[] = [];
  const block = topBlock(lines, "servers");
  if (block) {
    let name = "";
    for (let i = block.start + 1; i < block.end; i++) {
      const l = lines[i] ?? "";
      const n = /^ {2}([^\s#][^:]*):\s*$/.exec(l);
      if (n) name = unquote(n[1] ?? "");
      const a = /^ {4}address:\s*(.+?)\s*$/.exec(l);
      if (a && name) servers.push({ name, address: unquote(a[1] ?? "") });
    }
  }
  const p = priorities(lines);
  const order = p ? lines.slice(p.start, p.end).map((l) => unquote(l.replace(/^\s*-\s*/, ""))) : [];
  return { servers, try: order };
}

// writeBungeeBackends is config.yml with these backends (tried in this
// order) and ip_forward on. A backend already there keeps its motd and
// restricted settings.
export function writeBungeeBackends(yml: string, servers: Backend[], order: string[]): string {
  let lines = yml.split("\n");
  const old = topBlock(lines, "servers");
  const kept = new Map<string, string[]>();
  if (old) {
    let name = "";
    for (let i = old.start + 1; i < old.end; i++) {
      const l = lines[i] ?? "";
      const n = /^ {2}([^\s#][^:]*):\s*$/.exec(l);
      if (n) {
        name = unquote(n[1] ?? "");
        kept.set(name, []);
      } else if (name && /^ {4}\S/.test(l) && !/^ {4}address:/.test(l)) {
        kept.get(name)?.push(l);
      }
    }
  }
  const block = [
    servers.length === 0 ? "servers: {}" : "servers:",
    ...servers.flatMap((s) => [
      `  ${s.name}:`,
      ...(kept.get(s.name) ?? [`    motd: '${s.name}'`, "    restricted: false"]),
      `    address: ${s.address}`,
    ]),
  ];
  if (old) lines.splice(old.start, old.end - old.start, ...block);
  else lines = [...block, ...lines];
  const p = priorities(lines);
  if (p) lines.splice(p.start, p.end - p.start, ...order.map((n) => `${p.indent}- ${n}`));
  const fwd = lines.findIndex((l) => /^ip_forward:/.test(l));
  if (fwd >= 0) lines[fwd] = "ip_forward: true";
  else lines.push("ip_forward: true");
  return lines.join("\n");
}

// spigotForBungee is a backend's spigot.yml with settings.bungeecord on,
// or undefined when the file isn't laid out as Spigot writes it.
export function spigotForBungee(yml: string): string | undefined {
  const lines = yml.split("\n");
  const block = topBlock(lines, "settings");
  if (!block) return undefined;
  for (let i = block.start + 1; i < block.end; i++) {
    if (/^ {2}bungeecord:/.test(lines[i] ?? "")) {
      lines[i] = "  bungeecord: true";
      return lines.join("\n");
    }
  }
  lines.splice(block.start + 1, 0, "  bungeecord: true");
  return lines.join("\n");
}
