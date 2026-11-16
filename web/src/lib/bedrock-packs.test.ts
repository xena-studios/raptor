import assert from "node:assert/strict";
import { test } from "node:test";
import { deflateRawSync } from "node:zlib";

import {
  manifestOf,
  packFolder,
  packsIn,
  parseWorldPacks,
  withoutPack,
  withPack,
} from "./bedrock-packs.ts";
import { readZip } from "./zip.ts";

// zip builds a zip of named files (deflated), for tests.
function zip(files: Record<string, Uint8Array | string>): Uint8Array {
  const enc = (s: string) => new TextEncoder().encode(s);
  const parts: number[] = [];
  const central: number[] = [];
  const u16 = (a: number[], n: number) => a.push(n & 255, (n >> 8) & 255);
  const u32 = (a: number[], n: number) =>
    a.push(n & 255, (n >> 8) & 255, (n >> 16) & 255, (n >>> 24) & 255);
  let count = 0;
  for (const [name, content] of Object.entries(files)) {
    const raw = typeof content === "string" ? enc(content) : content;
    const data = deflateRawSync(raw);
    const nb = enc(name);
    const offset = parts.length;
    u32(parts, 0x04034b50);
    u16(parts, 20);
    u16(parts, 0);
    u16(parts, 8);
    u32(parts, 0);
    u32(parts, 0);
    u32(parts, data.length);
    u32(parts, raw.length);
    u16(parts, nb.length);
    u16(parts, 0);
    parts.push(...nb, ...data);
    u32(central, 0x02014b50);
    u16(central, 20);
    u16(central, 20);
    u16(central, 0);
    u16(central, 8);
    u32(central, 0);
    u32(central, 0);
    u32(central, data.length);
    u32(central, raw.length);
    u16(central, nb.length);
    u16(central, 0);
    u16(central, 0);
    u16(central, 0);
    u16(central, 0);
    u32(central, 0);
    u32(central, offset);
    central.push(...nb);
    count++;
  }
  const cdStart = parts.length;
  const out = [...parts, ...central];
  u32(out, 0x06054b50);
  u16(out, 0);
  u16(out, 0);
  u16(out, count);
  u16(out, count);
  u32(out, central.length);
  u32(out, cdStart);
  u16(out, 0);
  return new Uint8Array(out);
}

const manifest = (uuid: string, type: string, name = "My Pack") =>
  JSON.stringify({
    format_version: 2,
    header: { name, uuid, version: [1, 2, 0] },
    modules: [{ type, uuid: "00000000-0000-0000-0000-000000000000" }],
  });

const B = "11111111-2222-3333-4444-555555555555";
const R = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

test("reading packs' manifests", async () => {
  assert.equal(manifestOf(manifest(B, "data"))?.kind, "behavior");
  assert.equal(manifestOf(manifest(R, "resources"))?.kind, "resource");
  assert.equal(manifestOf(manifest(B, "script"))?.experimental, true);
  assert.equal(manifestOf("// a comment\n" + manifest(B, "data"))?.uuid, B);
  assert.equal(manifestOf("{}"), undefined);
  // A .mcpack with its manifest in a folder.
  const mcpack = zip({
    "MyPack/manifest.json": manifest(B, "data"),
    "MyPack/entities/x.json": "{}",
  });
  const single = await packsIn(mcpack);
  assert.deepEqual(
    single.map((p) => [p.uuid, p.kind, p.root]),
    [[B, "behavior", "MyPack"]],
  );
  // A .mcaddon with two .mcpack files inside.
  const addon = zip({
    "b.mcpack": zip({ "manifest.json": manifest(B, "data") }),
    "r.mcpack": zip({ "manifest.json": manifest(R, "resources") }),
  });
  const both = await packsIn(addon);
  assert.deepEqual(both.map((p) => [p.kind, p.root]).sort(), [
    ["behavior", ""],
    ["resource", ""],
  ]);
  assert.throws(() => readZip(new TextEncoder().encode("not a zip")));
});

test("a world's pack lists", () => {
  const list = parseWorldPacks(`[{"pack_id":"${B}","version":[1,0,0]}]`);
  const next = JSON.parse(withPack(list, { uuid: B, version: [1, 2, 0] }));
  assert.deepEqual(next, [{ pack_id: B, version: [1, 2, 0] }]);
  assert.deepEqual(JSON.parse(withoutPack(next, B.toUpperCase())), []);
  assert.deepEqual(parseWorldPacks("nope"), []);
  assert.equal(packFolder("../My Pack!", B), "_My Pack_-11111111");
});
