// readLines reads lines of JSON from a stream as they arrive, however the
// stream splits them into chunks, and hands each to onLine parsed. Blank
// lines are skipped; a last line without its newline still counts.
export async function readLines(
  body: ReadableStream<Uint8Array<ArrayBuffer>>,
  onLine: (value: unknown) => void,
): Promise<void> {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (value) {
      buf += value;
      let nl = buf.indexOf("\n");
      while (nl >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (line) onLine(JSON.parse(line));
        nl = buf.indexOf("\n");
      }
    }
    if (done) break;
  }
  if (buf.trim()) onLine(JSON.parse(buf));
}
