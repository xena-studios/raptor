// A small zip reader (#416): an archive's entries and their bytes, for
// reading Bedrock packs' manifests in the browser before uploading them.

export type ZipEntry = { name: string; method: number; size: number; data: Uint8Array };

const MAX_ENTRIES = 20_000;

// readZip lists a zip's entries (their compressed bytes, not yet inflated).
export function readZip(b: Uint8Array): ZipEntry[] {
  const v = new DataView(b.buffer, b.byteOffset, b.byteLength);
  // The end of central directory record, in the last 64 KiB.
  let eocd = -1;
  for (let i = b.length - 22; i >= Math.max(0, b.length - 65_557); i--) {
    if (v.getUint32(i, true) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error("That isn't a zip file.");
  const count = v.getUint16(eocd + 10, true);
  let p = v.getUint32(eocd + 16, true);
  if (count > MAX_ENTRIES) throw new Error("That archive has too many files.");
  const out: ZipEntry[] = [];
  for (let n = 0; n < count; n++) {
    if (p + 46 > b.length || v.getUint32(p, true) !== 0x02014b50)
      throw new Error("That zip file is damaged.");
    const method = v.getUint16(p + 10, true);
    const csize = v.getUint32(p + 20, true);
    const size = v.getUint32(p + 24, true);
    const nameLen = v.getUint16(p + 28, true);
    const extraLen = v.getUint16(p + 30, true);
    const commentLen = v.getUint16(p + 32, true);
    const local = v.getUint32(p + 42, true);
    const name = new TextDecoder().decode(b.subarray(p + 46, p + 46 + nameLen)).replace(/\\/g, "/");
    p += 46 + nameLen + extraLen + commentLen;
    if (local + 30 > b.length || v.getUint32(local, true) !== 0x04034b50)
      throw new Error("That zip file is damaged.");
    const start = local + 30 + v.getUint16(local + 26, true) + v.getUint16(local + 28, true);
    if (start + csize > b.length) throw new Error("That zip file is damaged.");
    out.push({ name, method, size, data: b.subarray(start, start + csize) });
  }
  return out;
}

// entryBytes is an entry's content: stored, or deflated.
export async function entryBytes(e: ZipEntry): Promise<Uint8Array> {
  if (e.method === 0) return e.data;
  if (e.method !== 8) throw new Error(`${e.name}: a compression this can't read.`);
  if (e.size > 64 << 20) throw new Error(`${e.name} is too big to read here.`);
  const stream = new Blob([e.data as BlobPart])
    .stream()
    .pipeThrough(new DecompressionStream("deflate-raw"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}
