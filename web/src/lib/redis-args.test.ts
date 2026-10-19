import assert from "node:assert/strict";
import { test } from "node:test";

import { splitArgs, ttlText } from "./redis-args.ts";

test("commands split like redis-cli", () => {
  assert.deepEqual(splitArgs("GET player:alex"), ["GET", "player:alex"]);
  assert.deepEqual(splitArgs('SET greeting "hello world"'), ["SET", "greeting", "hello world"]);
  assert.deepEqual(splitArgs(`HSET h f 'it''s'`), ["HSET", "h", "f", "its"]);
  assert.deepEqual(splitArgs('SET k "a \\"b\\" c\\n"'), ["SET", "k", 'a "b" c\n']);
  assert.deepEqual(splitArgs('SET k ""'), ["SET", "k", ""]);
  assert.deepEqual(splitArgs("  PING  "), ["PING"]);
});

test("times to live read as people say them", () => {
  assert.equal(ttlText(-1), "No expiry");
  assert.equal(ttlText(30_000), "Expires in 30 s");
  assert.equal(ttlText(5 * 60_000), "Expires in 5 min");
  assert.equal(ttlText(3 * 86_400_000), "Expires in 3 days");
});
