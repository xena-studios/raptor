// FiveM (#428): the backdoor scan's results (Wings' files.scan, rules in
// internal/wings/backdoor), and txAdmin's first-run PIN from the console.

export type Severity = "high" | "medium" | "low";

// One suspicious line, as Wings reports it.
export type ScanFinding = {
  file: string;
  line: number;
  rule: string;
  severity: Severity;
  // What the line does, in a sentence.
  title: string;
  // The part of the line that matched (decoded, if it hid it in escapes).
  excerpt: string;
};

// files.scan's answer.
export type ScanReport = {
  // The egg's folders that were there to scan.
  dirs: string[];
  files: number;
  bytes: number;
  // Files over the size limit, not read.
  skipped: number;
  // Why it ended early: "files", "bytes", "time", or "findings".
  stopped?: string;
  findings: ScanFinding[];
};

export const severityRank: Record<Severity, number> = { high: 0, medium: 1, low: 2 };

export type ScanFile = { file: string; severity: Severity; findings: ScanFinding[] };

// groupFindings puts the findings under their file: the worst files
// first, then by path; each file's lines in order.
export function groupFindings(findings: ScanFinding[]): ScanFile[] {
  const by = new Map<string, ScanFinding[]>();
  for (const f of findings) by.set(f.file, [...(by.get(f.file) ?? []), f]);
  const out = [...by].map(([file, fs]) => {
    const sorted = [...fs].sort((a, b) => a.line - b.line);
    const severity = sorted.reduce<Severity>(
      (worst, f) => (severityRank[f.severity] < severityRank[worst] ? f.severity : worst),
      "low",
    );
    return { file, severity, findings: sorted };
  });
  return out.sort(
    (a, b) => severityRank[a.severity] - severityRank[b.severity] || a.file.localeCompare(b.file),
  );
}

// counts is how many findings there are of each severity.
export function counts(findings: ScanFinding[]): Record<Severity, number> {
  const c = { high: 0, medium: 0, low: 0 };
  for (const f of findings) c[f.severity]++;
  return c;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// scanSummary is a scan's result in a sentence.
export function scanSummary(r: ScanReport): string {
  if (r.dirs.length === 0) return "There's no resources folder to scan yet.";
  const read = `${plural(r.files, "script")} in ${r.dirs.join(" and ")}`;
  if (r.findings.length === 0) return `Nothing suspicious in ${read}.`;
  const c = counts(r.findings);
  const parts = (["high", "medium", "low"] as const)
    .filter((s) => c[s] > 0)
    .map((s) => `${c[s]} ${s}`);
  const files = new Set(r.findings.map((f) => f.file)).size;
  return `${plural(r.findings.length, "suspicious line")} in ${plural(files, "file")} (${parts.join(", ")}), from ${read}.`;
}

// stoppedText says why a scan didn't read everything, or "" if it did.
export function stoppedText(r: ScanReport): string {
  const why: Record<string, string> = {
    files: "it reached the most files one scan reads",
    bytes: "it reached the most data one scan reads",
    time: "it ran out of time",
    findings: "it found too many suspicious lines to list",
  };
  const parts: string[] = [];
  if (r.stopped) parts.push(`The scan stopped early: ${why[r.stopped] ?? r.stopped}.`);
  if (r.skipped > 0) parts.push(`${plural(r.skipped, "file")} over 4 MiB weren't read.`);
  return parts.join(" ");
}

// splitPath is a file's folder and name, as files.delete takes them.
export function splitPath(file: string): { dir: string; name: string } {
  const i = file.lastIndexOf("/");
  return i < 0 ? { dir: "", name: file } : { dir: file.slice(0, i), name: file.slice(i + 1) };
}

// The console's colours and the box txAdmin draws around its banner.
// biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escapes
const ansi = /\x1b\[[0-9;]*[A-Za-z]/g;
const pinIntro = /PIN below to register|register.*\bPIN\b/i;
// The PIN alone on its line (inside the box), never part of a time.
const pinAlone = /(?:^|[\s│┃║|])(\d{4})(?=[\s│┃║|]*$)/;
const pinInline = /(?:\bPIN\b[^\d]*:\s*|addMaster\/pin#)(\d{4})\b/i;

// txAdminPin is the PIN txAdmin prints on its first run, to create the
// admin account with ("Use the PIN below to register:" with the PIN on
// the next line), the newest one in lines; undefined if there's none.
export function txAdminPin(lines: string[]): string | undefined {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = (lines[i] ?? "").replace(ansi, "");
    const inline = pinInline.exec(line);
    if (inline) return inline[1];
    if (!pinIntro.test(line)) continue;
    for (const next of lines.slice(i + 1, i + 4)) {
      const m = pinAlone.exec(next.replace(ansi, ""));
      if (m) return m[1];
    }
  }
  return undefined;
}
