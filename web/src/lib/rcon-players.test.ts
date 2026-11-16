import assert from "node:assert/strict";
import { test } from "node:test";

import { connectedFor, isRconGame, textLine, validId } from "./rcon-players.ts";

test("player IDs", () => {
  assert.ok(validId("source", "5"));
  assert.ok(validId("source", "STEAM_1:0:12345"));
  assert.ok(validId("source", "[U:1:999]", true));
  assert.ok(!validId("source", "5", true));
  assert.ok(!validId("source", "5; quit"));
  assert.ok(validId("ark", "76561198000000001"));
  assert.ok(validId("ark", "0002abcdef0123456789abcdef012345"));
  assert.ok(!validId("ark", "123"));
  assert.ok(validId("palworld", "steam_76561198000000001"));
  assert.ok(!validId("palworld", "steam 1"));
  assert.ok(validId("rust", "76561198000000001"));
  assert.ok(!validId("rust", "7656119800000000x"));
  assert.ok(isRconGame("rust"));
  assert.ok(!isRconGame("minecraft"));
});

test("text is one plain line", () => {
  assert.equal(textLine('  be "nice";\nplease \\ ', 100), "be nice please");
  assert.equal(Array.from(textLine("é".repeat(150), 120)).length, 120);
  assert.equal(textLine(' ;" ', 120), "");
});

test("connected for", () => {
  assert.equal(connectedFor(0), "");
  assert.equal(connectedFor(42), "42s");
  assert.equal(connectedFor(323), "5m");
  assert.equal(connectedFor(3723), "1h 02m");
});
