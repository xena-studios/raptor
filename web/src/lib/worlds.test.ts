import assert from "node:assert/strict";
import { test } from "node:test";
import { gzipSync } from "node:zlib";

import { gameRules, readGzipNbt, readNbt } from "./nbt.ts";
import { findWorlds, folderName, validRule, validRuleValue } from "./worlds.ts";

// nbt builds NBT bytes for a test: a root compound of compounds/strings.
function nbt(root: Record<string, unknown>): Uint8Array {
  const out: number[] = [];
  const u16 = (n: number) => out.push((n >> 8) & 255, n & 255);
  const str = (s: string) => {
    const b = new TextEncoder().encode(s);
    u16(b.length);
    out.push(...b);
  };
  const compound = (o: Record<string, unknown>) => {
    for (const [k, v] of Object.entries(o)) {
      if (typeof v === "string") {
        out.push(8);
        str(k);
        str(v);
      } else if (typeof v === "number") {
        out.push(3);
        str(k);
        out.push((v >>> 24) & 255, (v >>> 16) & 255, (v >>> 8) & 255, v & 255);
      } else {
        out.push(10);
        str(k);
        compound(v as Record<string, unknown>);
      }
    }
    out.push(0);
  };
  out.push(10);
  str("");
  compound(root);
  return new Uint8Array(out);
}

test("reading level.dat's game rules", async () => {
  const old = nbt({
    Data: { LevelName: "world", GameRules: { keepInventory: "false", randomTickSpeed: "3" } },
  });
  assert.deepEqual(gameRules(readNbt(old)), { keepInventory: "false", randomTickSpeed: "3" });
  // Newer worlds: typed values under game_rules; gzipped, as on disk.
  const neu = gzipSync(
    nbt({ Data: { game_rules: { "minecraft:keep_inventory": 1, spawn_mobs: 0 } } }),
  );
  assert.deepEqual(gameRules(await readGzipNbt(neu)), {
    "minecraft:keep_inventory": "1",
    spawn_mobs: "0",
  });
  assert.deepEqual(gameRules(readNbt(nbt({ Other: "x" }))), {});
  assert.throws(() => readNbt(new Uint8Array([8, 0, 0])));
});

test("finding worlds", () => {
  const dir = (path: string, names: string[]) => ({
    path,
    entries: names.map((n) => ({ name: n, type: n.includes(".") ? "file" : "dir" })),
  });
  const worlds = findWorlds([
    dir("world", ["level.dat", "region"]),
    dir("world_nether", ["level.dat", "DIM-1"]),
    dir("world_the_end", ["level.dat", "DIM1"]),
    dir("creative", ["level.dat"]),
    dir("plugins", ["LuckPerms.jar"]),
    dir("uploads/survival", ["level.dat"]),
  ]);
  assert.deepEqual(worlds, [
    { path: "creative", dimensions: [] },
    { path: "uploads/survival", dimensions: [] },
    { path: "world", dimensions: ["world_nether", "world_the_end"] },
  ]);
  assert.equal(folderName("My World (1).zip"), "My World _1_");
  // No way out of the server's folder.
  const sneaky = folderName("../../etc.zip");
  assert.ok(!sneaky.includes("/") && !sneaky.startsWith(".") && sneaky !== "..");
  assert.ok(validRule("keepInventory") && validRule("minecraft:keep_inventory"));
  assert.ok(!validRule("keep inventory; stop"));
  assert.ok(validRuleValue("true") && validRuleValue("3") && !validRuleValue("3; stop"));
});
