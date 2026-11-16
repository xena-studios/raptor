// Minecraft's server.properties values and § formatting codes, for the
// server icon and MOTD editor (#425).

// propertyValue is a key's value in a .properties file (Java's format:
// \uXXXX, \n, \t, \\ and escaped separators), or undefined if it isn't
// there.
export function propertyValue(text: string, key: string): string | undefined {
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*([^\s=:#!][^=:\s]*)\s*[=:]?\s?(.*)$/.exec(line);
    if (m && m[1] === key) return unescapeProperty(m[2] ?? "");
  }
  return undefined;
}

function unescapeProperty(s: string): string {
  return s.replace(/\\(u[0-9a-fA-F]{4}|.)/g, (_, c: string) => {
    if (c.length === 5) return String.fromCharCode(Number.parseInt(c.slice(1), 16));
    return ({ n: "\n", t: "\t", r: "\r", f: "\f" } as Record<string, string>)[c] ?? c;
  });
}

// escapeProperty writes a value as Minecraft does: non-ASCII (like §) as
// \uXXXX, so any reader gets it right whatever its charset.
export function escapeProperty(s: string): string {
  let out = "";
  for (const ch of s) {
    const c = ch.codePointAt(0) ?? 0;
    if (ch === "\\") out += "\\\\";
    else if (ch === "\n") out += "\\n";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\r") out += "\\r";
    else if (ch === "=" || ch === ":") out += `\\${ch}`;
    else if (c < 0x20 || c > 0x7e) {
      for (const unit of ch.split("")) {
        out += `\\u${unit.charCodeAt(0).toString(16).toUpperCase().padStart(4, "0")}`;
      }
    } else out += ch;
  }
  return out.startsWith(" ") ? `\\${out}` : out;
}

// withProperty is the file with key set to value: its line rewritten in
// place, or added at the end.
export function withProperty(text: string, key: string, value: string): string {
  const line = `${key}=${escapeProperty(value)}`;
  const lines = text.split("\n");
  const i = lines.findIndex((l) => {
    const m = /^\s*([^\s=:#!][^=:\s]*)\s*[=:]?/.exec(l);
    return m?.[1] === key;
  });
  if (i >= 0) {
    lines[i] = line + (lines[i]?.endsWith("\r") ? "\r" : "");
    return lines.join("\n");
  }
  const body = text === "" || text.endsWith("\n") ? text : `${text}\n`;
  return `${body}${line}\n`;
}

// The § colours, by code, as Minecraft draws them.
export const colours: Record<string, { name: string; hex: string }> = {
  "0": { name: "Black", hex: "#000000" },
  "1": { name: "Dark blue", hex: "#0000AA" },
  "2": { name: "Dark green", hex: "#00AA00" },
  "3": { name: "Dark aqua", hex: "#00AAAA" },
  "4": { name: "Dark red", hex: "#AA0000" },
  "5": { name: "Dark purple", hex: "#AA00AA" },
  "6": { name: "Gold", hex: "#FFAA00" },
  "7": { name: "Gray", hex: "#AAAAAA" },
  "8": { name: "Dark gray", hex: "#555555" },
  "9": { name: "Blue", hex: "#5555FF" },
  a: { name: "Green", hex: "#55FF55" },
  b: { name: "Aqua", hex: "#55FFFF" },
  c: { name: "Red", hex: "#FF5555" },
  d: { name: "Light purple", hex: "#FF55FF" },
  e: { name: "Yellow", hex: "#FFFF55" },
  f: { name: "White", hex: "#FFFFFF" },
};

export type Segment = {
  text: string;
  colour: string; // hex
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  strike?: boolean;
  obfuscated?: boolean;
};

// motdLines is a MOTD as the server list draws it: two lines at most, each
// a run of styled text. A colour code resets the formatting, as in the game.
export function motdLines(motd: string): Segment[][] {
  const base = colours["7"]?.hex ?? "#AAAAAA";
  return motd
    .split("\n")
    .slice(0, 2)
    .map((line) => {
      const out: Segment[] = [];
      let style: Omit<Segment, "text"> = { colour: base };
      let text = "";
      const flush = () => {
        if (text) out.push({ ...style, text });
        text = "";
      };
      for (let i = 0; i < line.length; i++) {
        const ch = line[i];
        const code = line[i + 1]?.toLowerCase();
        if (ch === "§" && code !== undefined) {
          flush();
          i++;
          const c = colours[code];
          if (c) style = { colour: c.hex };
          else if (code === "l") style = { ...style, bold: true };
          else if (code === "o") style = { ...style, italic: true };
          else if (code === "n") style = { ...style, underline: true };
          else if (code === "m") style = { ...style, strike: true };
          else if (code === "k") style = { ...style, obfuscated: true };
          else if (code === "r") style = { colour: base };
          continue;
        }
        text += ch;
      }
      flush();
      return out;
    });
}

// plainLength is how many characters a MOTD line shows (codes don't count).
export function plainLength(line: string): number {
  return line.replace(/§./g, "").length;
}
