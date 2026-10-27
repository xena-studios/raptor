// Advanced engine settings (docs/DATABASES.md#settings): name = value
// lines, checked here against the same rules Wings checks them with
// (internal/wings/databases/settings.go, which writes db-advanced.json), so
// a line that's wrong is said before saving. Wings checks again; it's the
// authority. Apart from databases.ts so the test runner can load it.

import advanced from "./db-advanced.json" with { type: "json" };

type Rule = {
  kind: "" | "int" | "real" | "bool" | "enum" | "set" | "size" | "duration" | "pattern";
  min: number;
  max: number;
  memory?: boolean;
  values?: string[];
  chars?: string;
  field?: string;
};

type Dialect = {
  separator: string;
  true: string;
  false: string;
  units: Record<string, number>;
  bare: boolean;
  case_units?: boolean;
};

const engines = advanced as unknown as Record<
  string,
  { dialect: Dialect; rules: Record<string, Rule> }
>;

export const maxAdvancedBytes = 4096;
export const maxAdvancedLines = 64;

export type AdvancedProblem = { line: number; message: string };

// hasAdvanced: the engine has advanced settings (MongoDB doesn't).
export function hasAdvanced(engine: string): boolean {
  return engine in engines;
}

// advancedNames are the settings an engine takes, for showing.
export function advancedNames(engine: string): string[] {
  const e = engines[engine];
  if (!e) return [];
  return Object.keys(e.rules)
    .filter((n) => !e.rules[n]?.field)
    .sort();
}

const settingName = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
const valueChars = /^[A-Za-z0-9_.,+$-]*$/;

// checkAdvanced checks an instance's advanced settings for an engine with
// memoryMiB of memory: the settings as the engine takes them, or the first
// problem (line 0 is the text as a whole).
export function checkAdvanced(
  engine: string,
  text: string,
  memoryMiB: number,
): { settings: [string, string][]; problem?: AdvancedProblem } {
  const settings: [string, string][] = [];
  if (text.trim() === "") return { settings };
  const e = engines[engine];
  if (!e)
    return { settings, problem: { line: 0, message: "This engine has no advanced settings" } };
  if (new TextEncoder().encode(text).length > maxAdvancedBytes) {
    return {
      settings,
      problem: { line: 0, message: `Advanced settings are at most ${maxAdvancedBytes} characters` },
    };
  }
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const n = i + 1;
    const fail = (message: string) => ({ settings: [], problem: { line: n, message } });
    const line = (lines[i] ?? "").trim();
    if (line === "" || line.startsWith("#")) continue;
    if (settings.length === maxAdvancedLines) return fail(`At most ${maxAdvancedLines} settings`);
    const eq = line.indexOf("=");
    if (eq < 0) return fail("Write it as name = value");
    let name = line.slice(0, eq).trim();
    let value = unquote(line.slice(eq + 1).trim());
    if (!settingName.test(name)) return fail(`"${name}" isn't a setting's name`);
    name = normalName(e.dialect, name);
    const rule = e.rules[name];
    if (!rule) return fail(`${name} isn't one of the settings that can be changed here`);
    if (rule.field) return fail(`${name} is the ${rule.field} field`);
    if (settings.some(([s]) => s === name)) return fail(`${name} is set twice`);
    if (rule.kind === "set") value = value.replace(/\s*,\s*/g, ",");
    if (!valueChars.test(value)) return fail("A value is letters, digits, and . , _ - + $ only");
    const checked = checkValue(e.dialect, rule, value, memoryMiB);
    if ("error" in checked) return fail(`${name}: ${checked.error}`);
    settings.push([name, checked.value]);
  }
  return { settings };
}

function unquote(v: string): string {
  if (v.length >= 2 && (v[0] === '"' || v[0] === "'") && v[v.length - 1] === v[0]) {
    return v.slice(1, -1).trim();
  }
  return v;
}

function normalName(d: Dialect, name: string): string {
  const lower = name.toLowerCase();
  return d.separator === "-" ? lower.replaceAll("_", "-") : lower.replaceAll("-", "_");
}

const KiB = 1024;
const MiB = 1024 * KiB;
const GiB = 1024 * MiB;
const TiB = 1024 * GiB;

function checkValue(
  d: Dialect,
  r: Rule,
  v: string,
  memoryMiB: number,
): { value: string } | { error: string } {
  switch (r.kind) {
    case "bool": {
      const l = v.toLowerCase();
      if (["on", "true", "yes", "1"].includes(l)) return { value: d.true };
      if (["off", "false", "no", "0"].includes(l)) return { value: d.false };
      return { error: `${d.true} or ${d.false}` };
    }
    case "enum": {
      const x = r.values?.find((x) => x.toLowerCase() === v.toLowerCase());
      return x !== undefined ? { value: x } : { error: `one of ${r.values?.join(", ")}` };
    }
    case "set": {
      if (v === "") return { value: "" };
      const out: string[] = [];
      for (const p of v.split(",")) {
        const x = r.values?.find((x) => x.toLowerCase() === p.trim().toLowerCase());
        if (x === undefined) return { error: `"${p.trim()}" isn't one of ${r.values?.join(", ")}` };
        out.push(x);
      }
      return { value: out.join(",") };
    }
    case "pattern":
      for (const c of v) {
        if (!r.chars?.includes(c)) return { error: `only the characters ${r.chars}` };
      }
      return { value: v };
    case "int":
    case "real": {
      const ok =
        r.kind === "int" ? /^-?[0-9]{1,15}$/ : /^-?([0-9]{1,15}(\.[0-9]{0,15})?|\.[0-9]{1,15})$/;
      const f = Number(v);
      if (!ok.test(v) || !Number.isFinite(f)) {
        return { error: r.kind === "int" ? "a whole number" : "a number" };
      }
      if (f < r.min || f > r.max) return { error: `between ${r.min} and ${r.max}` };
      return { value: v };
    }
    case "size": {
      const b = parseSize(d, v);
      if (typeof b === "string") return { error: b };
      let limit = r.max;
      if (r.memory && memoryMiB > 0) limit = Math.min(limit, memoryMiB * MiB);
      if (b < r.min || b > limit) {
        return {
          error: `between ${sizeText(r.min)} and ${sizeText(limit)}${limit < r.max ? ", the memory limit" : ""}`,
        };
      }
      return { value: v };
    }
    case "duration": {
      const ms = parseDuration(v);
      if (typeof ms === "string") return { error: ms };
      if (ms < r.min || ms > r.max)
        return { error: `between ${millis(r.min)} and ${millis(r.max)}` };
      return { value: v };
    }
  }
  return { error: "can't be set" };
}

function sizeExample(d: Dialect): string {
  if (d.case_units) return "128MB";
  return d.separator === "-" ? "64mb" : "128M";
}

function parseSize(d: Dialect, v: string): number | string {
  const m = /^([0-9]{1,15})([A-Za-z]{0,2})$/.exec(v);
  if (!m) return `a size, like ${sizeExample(d)}`;
  const n = Number(m[1]);
  const unit = m[2] ?? "";
  if (unit === "") return d.bare ? n : `a size with its unit, like ${sizeExample(d)}`;
  for (const [u, mul] of Object.entries(d.units)) {
    if (u === unit || (!d.case_units && u.toLowerCase() === unit.toLowerCase())) return n * mul;
  }
  return `a size, like ${sizeExample(d)}`;
}

const durationUnits: Record<string, number> = {
  us: 0.001,
  ms: 1,
  s: 1000,
  min: 60_000,
  h: 3_600_000,
  d: 86_400_000,
};

function parseDuration(v: string): number | string {
  const m = /^(-?[0-9]{1,10})(us|ms|s|min|h|d)?$/.exec(v);
  if (!m) return "a time, like 30s or 500ms";
  const n = Number(m[1]);
  if (!m[2]) return n === 0 || n === -1 ? n : "a time with its unit, like 30s or 500ms";
  if (n < 0) return "-1 for off, or a time like 30s";
  return n * (durationUnits[m[2]] ?? 1);
}

function sizeText(b: number): string {
  for (const [n, m] of [
    ["TB", TiB],
    ["GB", GiB],
    ["MB", MiB],
    ["kB", KiB],
  ] as const) {
    if (b >= m && b === Math.trunc(b / m) * m) return `${b / m} ${n}`;
  }
  if (b >= MiB) return `${Math.floor(b / MiB)} MB`;
  return `${b} bytes`;
}

function millis(ms: number): string {
  if (ms === -1) return "-1 (off)";
  if (ms >= 86_400_000 && ms === Math.trunc(ms / 86_400_000) * 86_400_000)
    return `${ms / 86_400_000}d`;
  if (ms >= 1000 && ms === Math.trunc(ms / 1000) * 1000) return `${ms / 1000}s`;
  return `${ms}ms`;
}
