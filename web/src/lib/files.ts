// The file manager's helpers. Kept free of imports so `node --test` can
// check them. Paths are relative to the server's directory, "" its root,
// with "/" between parts (Wings' files package resolves and confines them).

export function join(dir: string, name: string): string {
  return dir ? `${dir}/${name}` : name;
}

export function parent(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

// crumbs are a path's parts with the path up to each, for breadcrumbs.
export function crumbs(path: string): { name: string; path: string }[] {
  const out: { name: string; path: string }[] = [];
  let cur = "";
  for (const part of path.split("/").filter(Boolean)) {
    cur = join(cur, part);
    out.push({ name: part, path: cur });
  }
  return out;
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = bytes;
  let u = -1;
  do {
    v /= 1024;
    u++;
  } while (v >= 1024 && u < units.length - 1);
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[u]}`;
}

// The editor's limit, Wings' (files.MaxEdit).
export const maxEdit = 4 << 20;

// looksBinary: a NUL byte in the first 8 KB, as git decides.
export function looksBinary(data: Uint8Array): boolean {
  return data.subarray(0, 8192).includes(0);
}

const archives = [".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.zst", ".gz"];

export function isArchive(name: string): boolean {
  const n = name.toLowerCase();
  return archives.some((ext) => n.endsWith(ext));
}

// languageFor picks the editor's language (a Monaco language ID) from a
// file's name: the formats game servers' configs, scripts, and plugins come
// in.
export function languageFor(name: string): string {
  const n = name.toLowerCase();
  const ext = n.includes(".") ? n.slice(n.lastIndexOf(".") + 1) : "";
  if (n === "dockerfile") return "dockerfile";
  return languages[ext] ?? "plaintext";
}

const languages: Record<string, string> = {
  json: "json",
  json5: "json",
  mcmeta: "json",
  yml: "yaml",
  yaml: "yaml",
  xml: "xml",
  properties: "ini",
  lang: "ini",
  ini: "ini",
  cfg: "ini",
  conf: "ini",
  toml: "ini",
  sh: "shell",
  bash: "shell",
  bat: "bat",
  cmd: "bat",
  ps1: "powershell",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  py: "python",
  lua: "lua",
  java: "java",
  kt: "kotlin",
  html: "html",
  htm: "html",
  css: "css",
  md: "markdown",
  sql: "sql",
  go: "go",
  rs: "rust",
  cs: "csharp",
  php: "php",
  rb: "ruby",
};

// languageName is a language ID as the status bar shows it.
export function languageName(id: string): string {
  const names: Record<string, string> = {
    json: "JSON",
    yaml: "YAML",
    xml: "XML",
    ini: "Properties",
    shell: "Shell",
    bat: "Batch",
    powershell: "PowerShell",
    javascript: "JavaScript",
    typescript: "TypeScript",
    python: "Python",
    html: "HTML",
    css: "CSS",
    sql: "SQL",
    csharp: "C#",
    php: "PHP",
    plaintext: "Plain text",
  };
  return names[id] ?? id.charAt(0).toUpperCase() + id.slice(1);
}

// readOnlyByName: files the editor opens read-only (logs: the server writes
// them).
export function readOnlyByName(name: string): boolean {
  return /\.log(\.\d+)?$/i.test(name) || /\.log\.gz$/i.test(name);
}

// fromBase64 decodes a []byte from Go's JSON.
export function fromBase64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// validName: a single name, not a path, that Wings will accept.
export function validName(name: string): boolean {
  return (
    name !== "" && name !== "." && name !== ".." && !name.includes("/") && !name.includes("\0")
  );
}
