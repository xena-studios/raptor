// Readers and writers for the config files games list their Steam Workshop
// items in (x-raptor.workshop.file, docs/EGGS.md#steam-workshop, #408).
// Each writer changes only the list and leaves the rest of the file as it
// was: comments, order, spacing and settings Raptor doesn't know.

const idPattern = /^[0-9]{1,20}$/;

function unique(ids: string[]): string[] {
  return ids.filter((id, i) => ids.indexOf(id) === i);
}

// workshopPath fills {{VAR}} in a declared path with the server's variable
// values; undefined if a value is empty or would leave its folder.
export function workshopPath(path: string, value: (env: string) => string): string | undefined {
  let bad = false;
  const out = path.replace(/\{\{([A-Za-z0-9_]+)\}\}/g, (_, env: string) => {
    const v = value(env).trim();
    if (!v || v === "." || v === ".." || /[/\\\0]/.test(v)) bad = true;
    return v;
  });
  return bad ? undefined : out;
}

// ---- ini: a key=a;b;c line (Project Zomboid's WorkshopItems and Mods) ----

function iniLine(
  text: string,
  key: string,
): { start: number; end: number; value: string } | undefined {
  const re = /^[ \t]*([^=\r\n#;]+?)[ \t]*=[ \t]*(.*?)[ \t]*$/gm;
  for (const m of text.matchAll(re)) {
    if (m[1] === key) {
      const start = m.index ?? 0;
      return { start, end: start + m[0].length, value: m[2] ?? "" };
    }
  }
  return undefined;
}

// iniList is the list on the key's line, in order; undefined without one.
export function iniList(text: string, key: string, separator = ";"): string[] | undefined {
  const line = iniLine(text, key);
  if (!line) return undefined;
  return unique(
    line.value
      .split(separator)
      .map((s) => s.trim())
      .filter(Boolean),
  );
}

// setIniList writes the list on the key's line, or adds the line at the end.
export function setIniList(text: string, key: string, values: string[], separator = ";"): string {
  const next = `${key}=${values.join(separator)}`;
  const line = iniLine(text, key);
  if (line) return text.slice(0, line.start) + next + text.slice(line.end);
  const nl = text.includes("\r\n") ? "\r\n" : "\n";
  const sep = text === "" || text.endsWith("\n") ? "" : nl;
  return `${text}${sep}${next}${nl}`;
}

// zomboidModId is a mod ID written the way the file already writes them:
// Build 42 puts a backslash before each (\ModID), Build 41 doesn't.
export function zomboidModId(id: string, existing: string[]): string {
  const bare = id.replace(/^\\+/, "");
  return existing.some((m) => m.startsWith("\\")) ? `\\${bare}` : bare;
}

// modInfoId is the mod ID in a Project Zomboid mod.info (id=...).
export function modInfoId(text: string): string | undefined {
  const m = /^[ \t]*id[ \t]*=[ \t]*(.+?)[ \t]*$/im.exec(text);
  return m?.[1] || undefined;
}

// ---- json: a top-level array of IDs (Unturned's File_IDs) ----

// A JSON value's end, from its first character; strings are skipped whole.
function jsonValueEnd(text: string, i: number): number {
  let depth = 0;
  for (; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      i++;
      while (i < text.length && text[i] !== '"') i += text[i] === "\\" ? 2 : 1;
      if (depth === 0) return i + 1;
    } else if (c === "[" || c === "{") depth++;
    else if (c === "]" || c === "}") {
      depth--;
      if (depth === 0) return i + 1;
      if (depth < 0) return i;
    } else if (depth === 0 && (c === "," || /\s/.test(c ?? ""))) return i;
  }
  return i;
}

// The top-level key's value span; undefined if the object doesn't have it.
function jsonKey(text: string, key: string): { start: number; end: number } | undefined {
  let depth = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      const from = i;
      i++;
      while (i < text.length && text[i] !== '"') i += text[i] === "\\" ? 2 : 1;
      if (depth !== 1) continue;
      let j = i + 1;
      while (/\s/.test(text[j] ?? "")) j++;
      if (text[j] !== ":") continue;
      j++;
      while (/\s/.test(text[j] ?? "")) j++;
      let name: unknown;
      try {
        name = JSON.parse(text.slice(from, i + 1));
      } catch {
        name = undefined;
      }
      if (name === key) return { start: j, end: jsonValueEnd(text, j) };
      i = jsonValueEnd(text, j) - 1;
    } else if (c === "{" || c === "[") depth++;
    else if (c === "}" || c === "]") depth--;
  }
  return undefined;
}

// jsonIds are the IDs in the top-level key's array. Numbers are read as
// text: Workshop IDs are bigger than JavaScript's numbers hold exactly.
export function jsonIds(text: string, key: string): string[] | undefined {
  const span = jsonKey(text, key);
  if (!span) return undefined;
  const v = text.slice(span.start, span.end).trim();
  if (!v.startsWith("[")) return undefined;
  return unique(
    v
      .slice(1, -1)
      .split(",")
      .map((s) => s.trim().replace(/^"(.*)"$/, "$1"))
      .filter((s) => idPattern.test(s)),
  );
}

// setJsonIds writes the array, as numbers, in place of the key's value;
// it refuses a file that isn't a JSON object.
export function setJsonIds(text: string, key: string, ids: string[]): string {
  const open = text.indexOf("{");
  if (open < 0 || text.slice(0, open).trim() !== "")
    throw new Error("the file isn't a JSON object");
  const span = jsonKey(text, key);
  const lineStart = (at: number) => text.lastIndexOf("\n", at - 1) + 1;
  const nl = text.includes("\r\n") ? "\r\n" : "\n";
  if (span) {
    const indent = /^[ \t]*/.exec(text.slice(lineStart(span.start)))?.[0] ?? "";
    const inner = indent + (indent || "  ");
    const arr = ids.length
      ? `[${nl}${ids.map((id) => inner + id).join(`,${nl}`)}${nl}${indent}]`
      : "[]";
    return text.slice(0, span.start) + arr + text.slice(span.end);
  }
  // No key yet: the first one in the object.
  const empty = text
    .slice(open + 1)
    .trim()
    .startsWith("}");
  const arr = ids.length ? `[${nl}${ids.map((id) => `    ${id}`).join(`,${nl}`)}${nl}  ]` : "[]";
  const entry = `${nl}  ${JSON.stringify(key)}: ${arr}${empty ? nl : ","}`;
  return text.slice(0, open + 1) + entry + text.slice(open + 1);
}

// ---- dst: Don't Starve Together's two Lua files ----

// Lua's comments and strings, so a "{" or ServerModSetup inside one is
// skipped. Each part is code, a comment, or a string.
type LuaPart = { kind: "code" | "comment" | "string"; start: number; end: number };

function longBracket(text: string, i: number): number {
  // At "[": the level of a long bracket ([[, [==[), or -1.
  const m = /^\[(=*)\[/.exec(text.slice(i, i + 64));
  return m ? (m[1]?.length ?? 0) : -1;
}

function luaParts(text: string): LuaPart[] {
  const parts: LuaPart[] = [];
  let code = 0;
  const push = (kind: LuaPart["kind"], start: number, end: number) => {
    if (code < start) parts.push({ kind: "code", start: code, end: start });
    parts.push({ kind, start, end });
    code = end;
  };
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    if (c === "-" && text[i + 1] === "-") {
      const level = text[i + 2] === "[" ? longBracket(text, i + 2) : -1;
      let end: number;
      if (level >= 0) {
        const close = text.indexOf(`]${"=".repeat(level)}]`, i + 4 + level);
        end = close < 0 ? text.length : close + level + 2;
      } else {
        const nl = text.indexOf("\n", i);
        end = nl < 0 ? text.length : nl;
      }
      push("comment", i, end);
      i = end;
    } else if (c === '"' || c === "'") {
      let j = i + 1;
      while (j < text.length && text[j] !== c && text[j] !== "\n") j += text[j] === "\\" ? 2 : 1;
      push("string", i, Math.min(j + 1, text.length));
      i = j + 1;
    } else if (c === "[" && longBracket(text, i) >= 0) {
      const level = longBracket(text, i);
      const close = text.indexOf(`]${"=".repeat(level)}]`, i + 2 + level);
      const end = close < 0 ? text.length : close + level + 2;
      push("string", i, end);
      i = end;
    } else i++;
  }
  if (code < text.length) parts.push({ kind: "code", start: code, end: text.length });
  return parts;
}

// The text with comments blanked out (same length), so positions match.
function luaCode(text: string): string {
  let out = "";
  for (const p of luaParts(text)) {
    const s = text.slice(p.start, p.end);
    out += p.kind === "comment" ? s.replace(/[^\n]/g, " ") : s;
  }
  return out;
}

const setupCall = /ServerModSetup[ \t]*\([ \t]*(["'])([0-9]{1,20})\1[ \t]*\)/g;

// dstSetupIds are the items dedicated_server_mods_setup.lua downloads
// (ServerModSetup("id")), leaving out the commented-out examples.
export function dstSetupIds(text: string): string[] {
  return unique([...luaCode(text).matchAll(setupCall)].map((m) => m[2] ?? ""));
}

// setDstSetup keeps the calls for ids, removes the others (a call alone on
// its line takes the line with it), and adds new ones at the end.
// ServerModCollectionSetup and anything else stay as they are.
export function setDstSetup(text: string, ids: string[]): string {
  const code = luaCode(text);
  const have = new Set<string>();
  const cuts: [number, number][] = [];
  for (const m of code.matchAll(setupCall)) {
    const id = m[2] ?? "";
    const start = m.index ?? 0;
    const end = start + m[0].length;
    if (ids.includes(id) && !have.has(id)) {
      have.add(id);
      continue;
    }
    const ls = code.lastIndexOf("\n", start - 1) + 1;
    let le = code.indexOf("\n", end);
    if (le < 0) le = code.length;
    const alone = code.slice(ls, start).trim() === "" && code.slice(end, le).trim() === "";
    cuts.push(alone ? [ls, Math.min(le + 1, text.length)] : [start, end]);
  }
  let out = text;
  for (const [s, e] of cuts.reverse()) out = out.slice(0, s) + out.slice(e);
  const add = ids.filter((id) => !have.has(id));
  if (add.length === 0) return out;
  const nl = out.includes("\r\n") ? "\r\n" : "\n";
  const sep = out === "" || out.endsWith("\n") ? "" : nl;
  return out + sep + add.map((id) => `ServerModSetup("${id}")${nl}`).join("");
}

// The returned table's braces, and each "[\"workshop-N\"] = ..." entry in
// it: from its key to the separator after its value.
type OverrideEntry = { id: string; start: number; end: number };

function overrideTable(text: string): { open: number; close: number; entries: OverrideEntry[] } {
  const code = luaCode(text);
  const strings = new Uint8Array(text.length);
  for (const p of luaParts(text)) if (p.kind === "string") strings.fill(1, p.start, p.end);
  const inString = (i: number) => strings[i] === 1;
  const ret = /\breturn\s*\{/.exec(code);
  if (!ret) throw new Error("modoverrides.lua doesn't return a table");
  const open = ret.index + ret[0].length - 1;
  const entries: OverrideEntry[] = [];
  let depth = 0;
  let entry: OverrideEntry | undefined;
  for (let i = open; i < code.length; i++) {
    if (inString(i)) continue;
    const c = code[i];
    if (c === "{") depth++;
    else if (c === "}") {
      depth--;
      if (depth === 0) {
        // The last entry, without a separator: up to its value's end.
        let end = i;
        while (end > 0 && /\s/.test(code[end - 1] ?? "")) end--;
        if (entry) entries.push({ ...entry, end });
        return { open, close: i, entries };
      }
    } else if (depth === 1 && (c === "," || c === ";")) {
      if (entry) entries.push({ ...entry, end: i + 1 });
      entry = undefined;
    } else if (depth === 1 && c === "[" && !entry) {
      const m = /^\[\s*(["'])workshop-([0-9]{1,20})\1\s*\]\s*=/.exec(code.slice(i, i + 64));
      if (m) entry = { id: m[2] ?? "", start: i, end: i };
    }
  }
  throw new Error("modoverrides.lua's table isn't closed");
}

// dstOverrideIds are the items modoverrides.lua names (["workshop-id"]).
export function dstOverrideIds(text: string): string[] {
  if (text.trim() === "") return [];
  return unique(overrideTable(text).entries.map((e) => e.id));
}

// setDstOverrides removes the entries for items not in ids (with their
// settings) and turns new ones on; entries for ids already there, with
// their configuration_options, and everything else stay as they are.
export function setDstOverrides(text: string, ids: string[]): string {
  const nl = text.includes("\r\n") ? "\r\n" : "\n";
  const line = (id: string) => `  ["workshop-${id}"]={ enabled=true },${nl}`;
  if (text.trim() === "") return `return {${nl}${ids.map(line).join("")}}${nl}`;
  const t = overrideTable(text);
  const keep = new Set<string>();
  const cuts: [number, number][] = [];
  for (const e of t.entries) {
    if (ids.includes(e.id) && !keep.has(e.id)) {
      keep.add(e.id);
      continue;
    }
    // An entry alone on its lines takes them with it.
    const ls = text.lastIndexOf("\n", e.start - 1) + 1;
    let le = text.indexOf("\n", e.end);
    if (le < 0) le = text.length;
    const alone = text.slice(ls, e.start).trim() === "" && text.slice(e.end, le).trim() === "";
    cuts.push(alone ? [ls, Math.min(le + 1, text.length)] : [e.start, e.end]);
  }
  let out = text;
  let close = t.close;
  for (const [s, e] of cuts.reverse()) {
    out = out.slice(0, s) + out.slice(e);
    if (s < close) close -= e - s;
  }
  const add = ids.filter((id) => !keep.has(id));
  if (add.length === 0) return out;
  // The last entry needs a separator before ours.
  const code = luaCode(out);
  let last = close - 1;
  while (last > 0 && /\s/.test(code[last] ?? "")) last--;
  let at = close;
  if (code[last] !== "{" && code[last] !== "," && code[last] !== ";") {
    out = `${out.slice(0, last + 1)},${out.slice(last + 1)}`;
    at++;
  }
  const ls = out.lastIndexOf("\n", at - 1) + 1;
  const onOwnLine = out.slice(ls, at).trim() === "";
  const insert = add.map(line).join("");
  return onOwnLine
    ? out.slice(0, ls) + insert + out.slice(ls)
    : `${out.slice(0, at)}${nl}${insert}${out.slice(at)}`;
}
