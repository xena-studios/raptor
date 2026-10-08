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

export type Language = "json" | "yaml" | "xml" | "properties" | "toml" | "shell" | "ini" | "plain";

// languageFor picks syntax highlighting from a file's name: the formats
// game servers' configs come in.
export function languageFor(name: string): Language {
  const n = name.toLowerCase();
  const ext = n.slice(n.lastIndexOf(".") + 1);
  switch (ext) {
    case "json":
    case "json5":
    case "mcmeta":
      return "json";
    case "yml":
    case "yaml":
      return "yaml";
    case "xml":
      return "xml";
    case "properties":
    case "lang":
      return "properties";
    case "toml":
      return "toml";
    case "sh":
    case "bash":
      return "shell";
    case "ini":
    case "cfg":
    case "conf":
      return "ini";
  }
  return "plain";
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
