// Helpers for sharing a log on mclo.gs (components/share-log, #429). The
// Panel hides IP addresses itself; these only shape what's sent.

// maxLogBytes is the most of a log the Panel takes (mclogs.MaxBytes).
export const maxLogBytes = 1 << 20;

// shareableLog is a log without terminal colour codes, which mclo.gs
// would show as junk.
export function shareableLog(text: string): string {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escapes
  return text.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, "").replace(/\r\n?/g, "\n");
}

// logTail is a log's newest whole lines that fit in maxLogBytes (where a
// crash is), and whether older lines were left out.
export function logTail(
  text: string,
  max = maxLogBytes,
): { text: string; lines: number; cut: boolean } {
  const enc = new TextEncoder();
  const all = text.replace(/\n+$/, "").split("\n");
  if (all.length === 1 && all[0] === "") return { text: "", lines: 0, cut: false };
  const kept: string[] = [];
  let size = 0;
  for (let i = all.length - 1; i >= 0; i--) {
    const line = all[i] ?? "";
    const n = enc.encode(line).length + 1;
    if (size + n > max) break;
    kept.push(line);
    size += n;
  }
  kept.reverse();
  return { text: kept.join("\n"), lines: kept.length, cut: kept.length < all.length };
}
