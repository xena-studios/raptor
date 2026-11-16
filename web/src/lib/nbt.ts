// A small reader for Minecraft's NBT (#426): enough to read a world's
// level.dat (its game rules, seed, and name), never to write it.

export type Nbt =
  | number
  | bigint
  | string
  | Nbt[]
  | Uint8Array
  | Int32Array
  | BigInt64Array
  | { [k: string]: Nbt };

class Reader {
  private o = 0;
  private readonly b: Uint8Array;
  private readonly v: DataView;
  constructor(b: Uint8Array) {
    this.b = b;
    this.v = new DataView(b.buffer, b.byteOffset, b.byteLength);
  }
  u8() {
    return this.v.getUint8(this.o++);
  }
  i8() {
    return this.v.getInt8(this.o++);
  }
  i16() {
    const n = this.v.getInt16(this.o);
    this.o += 2;
    return n;
  }
  u16() {
    const n = this.v.getUint16(this.o);
    this.o += 2;
    return n;
  }
  i32() {
    const n = this.v.getInt32(this.o);
    this.o += 4;
    return n;
  }
  i64() {
    const n = this.v.getBigInt64(this.o);
    this.o += 8;
    return n;
  }
  f32() {
    const n = this.v.getFloat32(this.o);
    this.o += 4;
    return n;
  }
  f64() {
    const n = this.v.getFloat64(this.o);
    this.o += 8;
    return n;
  }
  str() {
    const n = this.u16();
    const s = new TextDecoder().decode(this.b.subarray(this.o, this.o + n));
    this.o += n;
    return s;
  }
  count() {
    const n = this.i32();
    if (n < 0 || n > 1 << 24) throw new Error("NBT: a list too long");
    return n;
  }
  payload(type: number, depth: number): Nbt {
    if (depth > 64) throw new Error("NBT: nested too deep");
    switch (type) {
      case 1:
        return this.i8();
      case 2:
        return this.i16();
      case 3:
        return this.i32();
      case 4:
        return this.i64();
      case 5:
        return this.f32();
      case 6:
        return this.f64();
      case 7: {
        const n = this.count();
        const out = this.b.slice(this.o, this.o + n);
        this.o += n;
        return out;
      }
      case 8:
        return this.str();
      case 9: {
        const t = this.u8();
        const n = this.count();
        const out: Nbt[] = [];
        for (let i = 0; i < n; i++) out.push(this.payload(t, depth + 1));
        return out;
      }
      case 10: {
        const out: { [k: string]: Nbt } = {};
        for (;;) {
          const t = this.u8();
          if (t === 0) return out;
          out[this.str()] = this.payload(t, depth + 1);
        }
      }
      case 11: {
        const n = this.count();
        const out = new Int32Array(n);
        for (let i = 0; i < n; i++) out[i] = this.i32();
        return out;
      }
      case 12: {
        const n = this.count();
        const out = new BigInt64Array(n);
        for (let i = 0; i < n; i++) out[i] = this.i64();
        return out;
      }
    }
    throw new Error(`NBT: unknown tag ${type}`);
  }
  root(): Nbt {
    const t = this.u8();
    if (t !== 10) throw new Error("NBT: not a compound");
    this.str();
    return this.payload(10, 0);
  }
}

// readNbt reads uncompressed NBT.
export function readNbt(b: Uint8Array): { [k: string]: Nbt } {
  return new Reader(b).root() as { [k: string]: Nbt };
}

// readGzipNbt reads gzipped NBT (level.dat), in a browser or Node.
export async function readGzipNbt(b: Uint8Array): Promise<{ [k: string]: Nbt }> {
  const stream = new Blob([b as BlobPart]).stream().pipeThrough(new DecompressionStream("gzip"));
  return readNbt(new Uint8Array(await new Response(stream).arrayBuffer()));
}

// gameRules are a level.dat's game rules, as name → value text: from
// Data.GameRules (string values, before 1.21.9) or Data.game_rules (typed
// values, after), whichever the world has.
export function gameRules(level: { [k: string]: Nbt }): Record<string, string> {
  const data = level.Data;
  if (!data || typeof data !== "object" || Array.isArray(data) || ArrayBuffer.isView(data))
    return {};
  const d = data as { [k: string]: Nbt };
  const rules = d.GameRules ?? d.game_rules;
  if (!rules || typeof rules !== "object" || Array.isArray(rules) || ArrayBuffer.isView(rules))
    return {};
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(rules as { [k: string]: Nbt })) {
    if (typeof v === "string" || typeof v === "number" || typeof v === "bigint") out[k] = String(v);
  }
  return out;
}
