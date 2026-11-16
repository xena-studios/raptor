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
