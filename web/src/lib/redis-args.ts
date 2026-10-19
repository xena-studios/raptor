// A pure helper, apart from db-documents.ts so the test runner can load it.

// splitArgs splits a Redis command line into its arguments as redis-cli
// does: spaces separate them, and double or single quotes keep one
// together (with \" and \\ inside double quotes).
export function splitArgs(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote = "";
  let has = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === "\\" && quote === '"' && i + 1 < line.length) {
        const n = line[++i];
        cur += n === "n" ? "\n" : n === "t" ? "\t" : n;
      } else if (c === quote) {
        quote = "";
      } else {
        cur += c;
      }
    } else if (c === '"' || c === "'") {
      quote = c;
      has = true;
    } else if (c === " " || c === "\t") {
      if (has) out.push(cur);
      cur = "";
      has = false;
    } else {
      cur += c;
      has = true;
    }
  }
  if (has) out.push(cur);
  return out;
}

// ttlText says how long a key has left: -1 is no expiry.
export function ttlText(ms: number): string {
  if (ms < 0) return "No expiry";
  const s = Math.round(ms / 1000);
  if (s < 60) return `Expires in ${s} s`;
  if (s < 3600) return `Expires in ${Math.round(s / 60)} min`;
  if (s < 86400) return `Expires in ${Math.round(s / 3600)} h`;
  return `Expires in ${Math.round(s / 86400)} days`;
}
